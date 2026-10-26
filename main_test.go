package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Check that duplicate lines are removed
// and unique lines remain in their original order.
func TestRemoveDuplicateLines(t *testing.T) {
	input := "apple\nbanana\napple\ncherry\nbanana"
	want := "apple\nbanana\ncherry"

	got := removeDuplicateLines(input)

	// Report a failure if the actual result differs.
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Check that the API uses duplicate-lines when
// the request doesn't specify a compression method.
func TestCompressHandlerDefaultsToDuplicateLines(t *testing.T) {
	// Create a request body without a "method" field.
	body := `{"text":"apple\nbanana\napple"}`

	// Build a fake POST request for our API handler.
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)

	// Record the response without starting the real server.
	recorder := httptest.NewRecorder()

	// Run the handler with our fake request and response.
	compressHandler(recorder, request)

	// The handler should respond successfully.
	if recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", recorder.Code, http.StatusOK)
	}

	// Decode the JSON response so we can inspect its fields.
	var response CompressResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	// The default method should remove the repeated "apple" line.
	if response.Method != "duplicate-lines" {
		t.Errorf("got method %q, want %q", response.Method, "duplicate-lines")
	}

	if response.CompressedText != "apple\nbanana" {
		t.Errorf("got compressed text %q, want %q", response.CompressedText, "apple\nbanana")
	}
}

// Check that the API rejects a compression method it doesn't recognize.
func TestCompressHandlerRejectsUnknownMethod(t *testing.T) {
	// Send a request with a method our API doesn't support.
	body := `{"text":"hello world","method":"not-a-real-method"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()

	// Run the handler and capture its response.
	compressHandler(recorder, request)

	// Unknown methods should receive HTTP 400 Bad Request.
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// Check that the API uses the summarizer when method is "ai-summary".
func TestCompressHandlerUsesAISummary(t *testing.T) {
	// Save the real summarizer so we can restore it after this test.
	originalSummarizer := summarizeText
	defer func() {
		summarizeText = originalSummarizer
	}()

	// Replace Ollama with a fake that always returns this short summary.
	summarizeText = func(text, apiKey string) (string, error) {
		if apiKey != "test-groq-key" {
			t.Errorf("unexpected API key passed to summarizer")
		}
		return "Short version", nil
	}

	// Ask the API to use the AI-summary method.
	body := `{"text":"This is a longer original sentence.","method":"ai-summary"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)
	request.Header.Set("X-Groq-API-Key", "test-groq-key")
	recorder := httptest.NewRecorder()

	// Run the handler with our fake summarizer.
	compressHandler(recorder, request)

	// The handler should respond successfully.
	if recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", recorder.Code, http.StatusOK)
	}

	// Decode the JSON response to inspect its fields.
	var response CompressResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	// Confirm the fake summary was returned and the method is correct.
	if response.CompressedText != "Short version" {
		t.Errorf("got compressed text %q, want %q", response.CompressedText, "Short version")
	}

	if response.Method != "ai-summary" {
		t.Errorf("got method %q, want %q", response.Method, "ai-summary")
	}
}

// Check that the API reports an error if the AI summarizer fails.
func TestCompressHandlerReturnsErrorWhenAISummaryFails(t *testing.T) {
	// Save the real summarizer and restore it when the test ends.
	originalSummarizer := summarizeText
	defer func() {
		summarizeText = originalSummarizer
	}()

	// Pretend the AI service failed.
	summarizeText = func(text, apiKey string) (string, error) {
		return "", fmt.Errorf("simulated Groq failure")
	}

	// Request AI summarization.
	body := `{"text":"Please summarize this.","method":"ai-summary"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)
	request.Header.Set("X-Groq-API-Key", "test-groq-key")
	recorder := httptest.NewRecorder()

	// Run the handler with the failing fake summarizer.
	compressHandler(recorder, request)

	// The handler should return 502 Bad Gateway.
	if recorder.Code != http.StatusBadGateway {
		t.Errorf("got status %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}

// Check that the API rejects a request with blank text.
func TestCompressHandlerRejectsEmptyText(t *testing.T) {
	// Send text containing only spaces.
	body := `{"text":"   "}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)
	recorder := httptest.NewRecorder()

	// Run the handler.
	compressHandler(recorder, request)

	// Blank text should receive HTTP 400 Bad Request.
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// Check that the API rejects malformed JSON.
func TestCompressHandlerRejectsInvalidJSON(t *testing.T) {
	// This is deliberately invalid JSON: the closing quote is missing.
	body := `{"text":"hello}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)
	recorder := httptest.NewRecorder()

	// Run the handler.
	compressHandler(recorder, request)

	// Invalid JSON should receive HTTP 400 Bad Request.
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

// Check that the API rejects requests that are not POST.
func TestCompressHandlerRejectsGET(t *testing.T) {
	// Create a GET request instead of a POST request.
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/compress",
		nil,
	)
	recorder := httptest.NewRecorder()

	// Run the handler.
	compressHandler(recorder, request)

	// GET should receive HTTP 405 Method Not Allowed.
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

// Check that the API counts Unicode characters correctly.
func TestCompressHandlerCountsUnicodeCharacters(t *testing.T) {
	// "é" is one visible character, even though it uses multiple UTF-8 bytes.
	body := `{"text":"café","method":"duplicate-lines"}`

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(body),
	)
	recorder := httptest.NewRecorder()

	compressHandler(recorder, request)

	var response CompressResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	// "café" has four Unicode characters.
	if response.OriginalChars != 4 {
		t.Errorf("got original_chars %d, want 4", response.OriginalChars)
	}
}

func TestCompressHandlerRejectsOversizedRequest(t *testing.T) {
	// Create text larger than the 1 MiB request limit.
	largeText := strings.Repeat("a", (1<<20)+1)

	// Put the large text into a valid JSON request.
	body, err := json.Marshal(CompressRequest{
		Text:   largeText,
		Method: "duplicate-lines",
	})
	if err != nil {
		t.Fatalf("could not create request body: %v", err)
	}

	// Send the oversized request to the handler.
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/compress",
		strings.NewReader(string(body)),
	)
	recorder := httptest.NewRecorder()

	// Run the handler.
	compressHandler(recorder, request)

	// Oversized requests should receive HTTP 413.
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf(
			"got status %d, want %d",
			recorder.Code,
			http.StatusRequestEntityTooLarge,
		)
	}
}
