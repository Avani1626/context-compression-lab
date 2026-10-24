package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// CompressRequest is the JSON the client sends to our API.
type CompressRequest struct {
    Text   string `json:"text"`
    Method string `json:"method"`
}

// CompressResponse is the JSON our API sends back.
type CompressResponse struct {
	OriginalText      string  `json:"original_text"`
	CompressedText    string  `json:"compressed_text"`
	OriginalChars     int     `json:"original_chars"`
	CompressedChars   int     `json:"compressed_chars"`
	ReductionPercent  float64 `json:"reduction_percent"`
	Method            string  `json:"method"`
}

// removeDuplicateLines is our current baseline compressor.
// Later, we'll replace or supplement it with an LLM-generated summary.
func removeDuplicateLines(text string) string {
	lines := strings.Split(text, "\n")
	seen := make(map[string]bool)
	kept := make([]string, 0, len(lines))

	for _, line := range lines {
		// Ignore a line if we've already kept an identical one.
		if seen[line] {
			continue
		}

		seen[line] = true
		kept = append(kept, line)
	}

	// Join the unique lines back into text.
	return strings.Join(kept, "\n")
}

// These fields describe the JSON request Ollama expects.
type ollamaGenerateRequest struct {
    Model  string `json:"model"`
    Prompt string `json:"prompt"`
    Stream bool   `json:"stream"`
}

// This field holds the generated text from Ollama's JSON response.
type ollamaGenerateResponse struct {
    Response string `json:"response"`
}
// summarizeText is the summarizer used by the API handler.
// It points to Ollama by default, but tests can temporarily replace it.
var summarizeText = summarizeWithOllama
// summarizeWithOllama asks the local Phi-3 model to summarize text.
func summarizeWithOllama(text string) (string, error) {
    // Tell the model to make the text shorter without inventing facts.
    prompt := "Rewrite the text as a shorter summary. Keep only its essential facts. Do not add an introduction, repeat the task, or include information not present in the text. If the text is already concise, preserve it rather than making it longer.\n\nText:\n" + text

    // Prepare the request using our locally downloaded model.
    requestData := ollamaGenerateRequest{
        Model:  "phi3:latest",
        Prompt: prompt,
        Stream: false,
    }

    // Convert the Go request structure into JSON.
    jsonData, err := json.Marshal(requestData)
    if err != nil {
        return "", fmt.Errorf("create Ollama request JSON: %w", err)
    }

    // Build an HTTP request to Ollama's local text-generation endpoint.
    request, err := http.NewRequest(
        http.MethodPost,
        "http://localhost:11434/api/generate",
        bytes.NewReader(jsonData),
    )
    if err != nil {
        return "", fmt.Errorf("build Ollama request: %w", err)
    }

    // Tell Ollama that the request body is JSON.
    request.Header.Set("Content-Type", "application/json")

    // Allow up to two minutes for the local model to generate its summary.
    client := &http.Client{
        Timeout: 2 * time.Minute,
    }

    // Send the request and wait for Ollama's reply.
    httpResponse, err := client.Do(request)
    if err != nil {
        return "", fmt.Errorf("send request to Ollama: %w", err)
    }
    defer httpResponse.Body.Close()

    // Report an error if Ollama returned an unsuccessful HTTP status.
    if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
        return "", fmt.Errorf("Ollama returned HTTP status %s", httpResponse.Status)
    }

    // Decode Ollama's JSON reply into our response structure.
    var result ollamaGenerateResponse
    if err := json.NewDecoder(httpResponse.Body).Decode(&result); err != nil {
        return "", fmt.Errorf("read Ollama response JSON: %w", err)
    }

    // Remove extra whitespace around the generated summary.
    summary := strings.TrimSpace(result.Response)
    if summary == "" {
        return "", fmt.Errorf("Ollama returned an empty summary")
    }

    return summary, nil
}

// compressHandler handles POST requests to /api/compress.
func compressHandler(w http.ResponseWriter, r *http.Request) {
	// This endpoint only accepts POST requests.
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST for this endpoint", http.StatusMethodNotAllowed)
		return
	}

	// Decode the JSON request body into our request struct.
	var request CompressRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Reject empty text so the API doesn't process a blank request.
	if strings.TrimSpace(request.Text) == "" {
		http.Error(w, "Text cannot be empty", http.StatusBadRequest)
		return
	}

	// Use duplicate-line compression when no method was specified.
method := request.Method
if method == "" {
    method = "duplicate-lines"
}

// Choose the compression method requested by the caller.
var compressed string

switch method {
case "duplicate-lines":
    compressed = removeDuplicateLines(request.Text)

case "ai-summary":
    // Ask Ollama to summarize the submitted text.
    compressed, err = summarizeText(request.Text)
    if err != nil {
        // Log the technical error for us to debug.
        log.Printf("Ollama summarization failed: %v", err)

        // Tell the API caller that the AI service failed.
        http.Error(w, "Could not generate AI summary", http.StatusBadGateway)
        return
    }

default:
    http.Error(w, "Unknown compression method", http.StatusBadRequest)
    return
}

// Count Unicode runes so these values match the "chars" field names.
originalChars := utf8.RuneCountInString(request.Text)
compressedChars := utf8.RuneCountInString(compressed)

	reduction := 0.0
	if originalChars > 0 {
		reduction = float64(originalChars-compressedChars) /
			float64(originalChars) * 100
	}

	// Prepare the response as JSON.
	response := CompressResponse{
		OriginalText:     request.Text,
		CompressedText:   compressed,
		OriginalChars:    originalChars,
		CompressedChars:  compressedChars,
		ReductionPercent: reduction,
		Method: method,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Could not encode response: %v", err)
	}
}

func main() {
	// Connect the API route to its handler.
	http.HandleFunc("/api/compress", compressHandler)

	http.Handle("/", http.FileServer(http.Dir("./frontend")))

	// Start the local web server.
	fmt.Println("Context Compression API running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}