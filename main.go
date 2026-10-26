package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
	 "io"
	 "errors" // Lets us identify specific errors.
)

// CompressRequest is the JSON the client sends to our API.
type CompressRequest struct {
	Text   string `json:"text"`
	Method string `json:"method"`
}

// CompressResponse is the JSON our API sends back.
type CompressResponse struct {
	OriginalText     string  `json:"original_text"`
	CompressedText   string  `json:"compressed_text"`
	OriginalChars    int     `json:"original_chars"`
	CompressedChars  int     `json:"compressed_chars"`
	ReductionPercent float64 `json:"reduction_percent"`
	Method           string  `json:"method"`
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

type groqChatRequest struct {
	Model    string        `json:"model"`
	Messages []groqMessage `json:"messages"`
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqChatResponse struct {
	Choices []struct {
		Message groqMessage `json:"message"`
	} `json:"choices"`
}

// summarizeText calls Groq using the key supplied for this request.
var summarizeText func(text, apiKey string) (string, error) = summarizeWithGroq

func summarizeWithGroq(text, apiKey string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("Groq API key is required")
	}

	prompt := "Rewrite the text as a shorter summary. Keep its essential facts. Do not invent information. If the text is already concise, preserve it rather than making it longer.\n\nText:\n" + text

	requestData := groqChatRequest{
		Model: "openai/gpt-oss-120b",
		Messages: []groqMessage{
			{
				Role:    "system",
				Content: "You summarize text accurately and concisely without inventing facts.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return "", fmt.Errorf("create Groq request: %w", err)
	}

	request, err := http.NewRequest(
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		strings.NewReader(string(jsonData)),
	)
	if err != nil {
		return "", fmt.Errorf("build Groq request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	httpResponse, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("send request to Groq")
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
    errorBody, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
    return "", fmt.Errorf(
        "Groq returned HTTP status %d: %s",
        httpResponse.StatusCode,
        strings.TrimSpace(string(errorBody)),
    )
}

	var result groqChatResponse
	if err := json.NewDecoder(httpResponse.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("read Groq response")
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("Groq returned no summary")
	}

	summary := strings.TrimSpace(result.Choices[0].Message.Content)
	if summary == "" {
		return "", fmt.Errorf("Groq returned an empty summary")
	}

	return summary, nil
}

// summarizeWithOllama asks the local Phi-3 model to summarize text.

// compressHandler handles POST requests to /api/compress.
func compressHandler(w http.ResponseWriter, r *http.Request) {
	// This endpoint only accepts POST requests.
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST for this endpoint", http.StatusMethodNotAllowed)
		return
	}

	// Decode the JSON request body into our request struct.
	// Limit request bodies to 1 MiB to prevent oversized requests.
r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

// Decode the JSON request body into our request struct.
// Limit the request body to 1 MiB (protects against huge requests).
r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

// Create a variable to hold the incoming JSON data.
var request CompressRequest

// Read the JSON and put its values into request.
err := json.NewDecoder(r.Body).Decode(&request)

// Check whether reading the JSON failed.
if err != nil {
    // Check whether the request was too large.
    var maxBytesError *http.MaxBytesError
    if errors.As(err, &maxBytesError) {
        // Return HTTP 413 when the request exceeds 1 MiB.
        http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
        return
    }

    // Return HTTP 400 when the JSON is invalid.
    http.Error(w, "Invalid JSON body", http.StatusBadRequest)
    return
}

// Reject empty text.
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
		apiKey := strings.TrimSpace(r.Header.Get("X-Groq-API-Key"))
		if apiKey == "" {
			http.Error(w, "Groq API key is required for AI summary", http.StatusBadRequest)
			return
		}

		compressed, err = summarizeText(request.Text, apiKey)
		if err != nil {
			// Log only a generic message; never log the API key or provider details.
			log.Printf("Groq summarization failed: %v", err)
			http.Error(w, "Could not generate AI summary. Check your API key and try again.", http.StatusBadGateway)
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
		Method:           method,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Could not encode response: %v", err)
	}
}

func main() {
	http.HandleFunc("/api/compress", compressHandler)
	http.Handle("/", http.FileServer(http.Dir("./frontend")))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Context Compression API running on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
