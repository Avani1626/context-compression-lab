# Context Compression Lab

A lightweight text compression web app built with Go, HTML, CSS, and JavaScript. Compare simple duplicate-line removal with AI-powered summarization, and see how much text and how many estimated tokens are saved.

**Live demo:** https://context-compression-lab.onrender.com

**GitHub:** https://github.com/Avani1626/context-compression-lab

## Features

- **Duplicate-line removal:** Removes exact repeated lines while preserving their original order.
- **AI summarization:** Uses the Groq API to generate a shorter version of the input.
- **Compression metrics:** Shows original and compressed character counts, percentage reduction, estimated token counts, tokens saved, and estimated token reduction.
- **Request size limit:** Rejects request bodies larger than 1 MiB.
- **Rate limiting:** Includes an in-memory request limiter.
- **Automated tests:** Includes Go tests for compression and API behavior.

## Requirements

- Go installed
- A Groq API key to use AI summarization

Duplicate-line removal does not require an API key. For AI summarization, enter your Groq API key in the app when prompted. The key is sent to the backend to make requests to Groq; use your own key and keep it private.

## Run locally

From the project directory, run:

```powershell
go run .
```

Open http://localhost:8080 in your browser.

The app uses port `8080` by default. Set the `PORT` environment variable if you need a different port.

## Run tests

```powershell
go test ./...
```

## API

### `POST /api/compress`

Example request:

```json
{
  "text": "A line of text.\nA line of text.",
  "method": "duplicate-lines"
}
```

Available methods:

- `duplicate-lines`
- `ai-summary`

For `ai-summary`, send your Groq API key in the `X-Groq-API-Key` request header.

The response includes the compressed text, character metrics, estimated token metrics, tokenizer label, and selected method.

## Token-count accuracy

Token counts use the `o200k_base` tokenizer as an estimate for `gpt-oss-120b`. They are **estimates, not exact token counts** for every model or API request.

## Limitations

- AI summaries may omit important details or occasionally be longer than the input. Review summaries before relying on them.
- Rate limiting is in-memory and may not identify individual visitors correctly behind a reverse proxy. It is a basic safeguard, not production-grade abuse protection.
- The hosted app may take a little time to respond after inactivity on a free hosting tier.
