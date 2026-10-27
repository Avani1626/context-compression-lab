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

	// Check that the original text has a positive token count.
	if response.OriginalTokens <= 0 {
		t.Errorf("got original tokens %d, want more than 0", response.OriginalTokens)
	}

	// Check that the compressed text has a positive token count.
	if response.CompressedTokens <= 0 {
		t.Errorf("got compressed tokens %d, want more than 0", response.CompressedTokens)
	}

	// Check that tokens saved equals original tokens minus compressed tokens.
	wantTokensSaved := response.OriginalTokens - response.CompressedTokens
	if response.TokensSaved != wantTokensSaved {
		t.Errorf("got tokens saved %d, want %d", response.TokensSaved, wantTokensSaved)
	}

	// Check that the API identifies the tokenizer and its estimate.
	if response.Tokenizer != "o200k_base (estimate for gpt-oss-120b)" {
		t.Errorf("got tokenizer %q, want the o200k_base estimate label", response.Tokenizer)
	}

	// Check that the reduction percentage matches the returned token counts.
	wantReduction := float64(wantTokensSaved) / float64(response.OriginalTokens) * 100
	if response.TokenReductionPercent != wantReduction {
		t.Errorf("got token reduction %.2f, want %.2f", response.TokenReductionPercent, wantReduction)
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

func TestRateLimitMiddleware(t *testing.T) {
	// Reset the shared rate-limit data so this test starts fresh.
	rateLimitMu.Lock()
	rateLimitClients = make(map[string]rateLimitEntry)
	rateLimitMu.Unlock()

	// Create a test handler that returns HTTP 200 for allowed requests.
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Put the rate limiter in front of the test handler.
	handler := rateLimitMiddleware(nextHandler)

	// Send 31 requests from the same simulated client IP.
	for i := 1; i <= 31; i++ {
		request := httptest.NewRequest(http.MethodGet, "/", nil)

		// Give every request the same IP so they share one counter.
		request.RemoteAddr = "192.0.2.1:12345"

		// Capture the HTTP response without starting a real server.
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		// The first 30 requests should pass; the 31st should be rejected.
		if i <= 30 && recorder.Code != http.StatusOK {
			t.Errorf("request %d: got status %d, want %d", i, recorder.Code, http.StatusOK)
		}

		if i == 31 && recorder.Code != http.StatusTooManyRequests {
			t.Errorf("request %d: got status %d, want %d", i, recorder.Code, http.StatusTooManyRequests)
		}
	}
}

func TestCountEstimatedTokens(t *testing.T) {
	// Count tokens in a normal sentence.
	count, err := countEstimatedTokens("Hello, world!")

	// Fail the test if token counting returned an error.
	if err != nil {
		t.Fatalf("countEstimatedTokens returned an error: %v", err)
	}

	// A non-empty sentence should produce at least one token.
	if count <= 0 {
		t.Errorf("got %d tokens, want a count greater than zero", count)
	}

	// Count tokens in an empty string.
	emptyCount, err := countEstimatedTokens("")

	// Fail the test if counting empty text returned an error.
	if err != nil {
		t.Fatalf("countEstimatedTokens returned an error for empty text: %v", err)
	}

	// Empty text should contain zero tokens.
	if emptyCount != 0 {
		t.Errorf("got %d tokens for empty text, want 0", emptyCount)
	}
}
