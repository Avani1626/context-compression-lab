package main

import (
	"encoding/json"
	"errors" // Lets us identify specific errors.
	"fmt"
	"github.com/tiktoken-go/tokenizer"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync" // Provides a mutex to safely share rate-limit data between requests.
	"time"
	"unicode/utf8"
)

// countEstimatedTokens counts text tokens using the o200k_base encoding.
func countEstimatedTokens(text string) (int, error) {
	// Get the tokenizer for the o200k_base encoding.
	codec, err := tokenizer.Get(tokenizer.O200kBase)

	// Stop if the tokenizer could not be loaded.
	if err != nil {
		return 0, err
	}

	// Count the tokens in the provided text.
	count, err := codec.Count(text)

	// Return an error if token counting failed.
	if err != nil {
		return 0, err
	}

	// Return the estimated token count.
	return count, nil
}

// CompressRequest is the JSON the client sends to our API.
type CompressRequest struct {
	Text   string `json:"text"`
	Method string `json:"method"`
}

// CompressResponse is the JSON our API sends back.

type CompressResponse struct {
	OriginalText          string  `json:"original_text"`
	CompressedText        string  `json:"compressed_text"`
	OriginalChars         int     `json:"original_chars"`
	CompressedChars       int     `json:"compressed_chars"`
	ReductionPercent      float64 `json:"reduction_percent"`
	OriginalTokens        int     `json:"original_tokens"`
	CompressedTokens      int     `json:"compressed_tokens"`
	TokensSaved           int     `json:"tokens_saved"`
	TokenReductionPercent float64 `json:"token_reduction_percent"`
	Tokenizer             string  `json:"tokenizer"`
	Method                string  `json:"method"`
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

	// Estimate the number of tokens in the original text.
	originalTokens, err := countEstimatedTokens(request.Text)
	if err != nil {
		log.Printf("Could not count original tokens: %v", err)
		http.Error(w, "Could not estimate token count", http.StatusInternalServerError)
		return
	}

	// Estimate the number of tokens in the compressed text.
	compressedTokens, err := countEstimatedTokens(compressed)
	if err != nil {
		log.Printf("Could not count compressed tokens: %v", err)
		http.Error(w, "Could not estimate token count", http.StatusInternalServerError)
		return
	}

	// Calculate how many tokens the compression saved.
	tokensSaved := originalTokens - compressedTokens

	// Calculate the percentage of tokens reduced.
	tokenReduction := 0.0
	if originalTokens > 0 {
		tokenReduction = float64(tokensSaved) /
			float64(originalTokens) * 100
	}

	reduction := 0.0
	if originalChars > 0 {
		reduction = float64(originalChars-compressedChars) /
			float64(originalChars) * 100
	}

	// Prepare the response as JSON.
	response := CompressResponse{
		OriginalText:          request.Text,
		CompressedText:        compressed,
		OriginalChars:         originalChars,
		CompressedChars:       compressedChars,
		ReductionPercent:      reduction,
		Method:                method,
		OriginalTokens:        originalTokens,
		CompressedTokens:      compressedTokens,
		TokensSaved:           tokensSaved,
		TokenReductionPercent: tokenReduction,
		Tokenizer:             "o200k_base (estimate for gpt-oss-120b)",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Could not encode response: %v", err)
	}
}

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

// rateLimitMu protects the shared map from concurrent access.
var rateLimitMu sync.Mutex

// rateLimitClients stores request counts for each client IP address.
var rateLimitClients = make(map[string]rateLimitEntry)

// rateLimitMiddleware limits each IP address to 30 requests per minute.
func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the client's IP address from the network connection.
		clientIP := r.RemoteAddr

		// Check and update the rate-limit count while holding the mutex.
		limited := func() bool {
			rateLimitMu.Lock()
			defer rateLimitMu.Unlock()

			// Get the current time for checking the one-minute window.
			now := time.Now()

			// Look up this client's existing rate-limit entry.
			entry := rateLimitClients[clientIP]

			// Reset the count if this is a new or expired one-minute window.
			if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= time.Minute {
				entry = rateLimitEntry{windowStart: now}
			}

			// Reject this request if the client has reached the limit.
			if entry.count >= 30 {
				return true
			}

			// Count this request and save the updated entry.
			entry.count++
			rateLimitClients[clientIP] = entry

			return false
		}()

		// Return HTTP 429 if the client exceeded the limit.
		if limited {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Rate limit exceeded. Try again in one minute.", http.StatusTooManyRequests)
			return
		}

		// Run the original handler after the rate-limit check.
		next.ServeHTTP(w, r)
	})
}

// cleanupRateLimitEntries periodically removes expired client entries.
func cleanupRateLimitEntries() {
	// Run cleanup once every minute.
	ticker := time.NewTicker(time.Minute)

	// Stop the ticker if this function ever returns.
	defer ticker.Stop()

	// Keep cleaning while the server is running.
	for range ticker.C {
		// Get the current time for checking expired entries.
		now := time.Now()

		// Lock the shared map while deleting entries.
		rateLimitMu.Lock()

		// Check each client's rate-limit entry.
		for clientIP, entry := range rateLimitClients {
			// Remove entries whose one-minute window has expired.
			if now.Sub(entry.windowStart) >= time.Minute {
				delete(rateLimitClients, clientIP)
			}
		}

		// Allow other requests to access the map again.
		rateLimitMu.Unlock()
	}
}
func main() {
	go cleanupRateLimitEntries()

	http.Handle("/api/compress", rateLimitMiddleware(http.HandlerFunc(compressHandler)))
	http.Handle("/", http.FileServer(http.Dir("./frontend")))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Context Compression API running on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
