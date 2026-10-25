
const inputText = document.getElementById("input-text");
const methodSelect = document.getElementById("compression-method");
const compressButton = document.getElementById("compress-button");

const resultPlaceholder = document.getElementById("result-placeholder");
const resultOutput = document.getElementById("result");
const compressionStats = document.getElementById("compression-stats");


const groqKeyPanel = document.getElementById("groq-key-panel");
const groqApiKeyInput = document.getElementById("groq-api-key");

function updateGroqKeyPanel() {
  const isAiSummary = methodSelect.value === "ai-summary";
  groqKeyPanel.hidden = !isAiSummary;
}

methodSelect.addEventListener("change", updateGroqKeyPanel);

// Restore the key for this browser tab session, if one exists.
groqApiKeyInput.value = sessionStorage.getItem("groq-api-key") || "";

// Keep the key in session storage, not in your backend.
groqApiKeyInput.addEventListener("input", () => {
  sessionStorage.setItem("groq-api-key", groqApiKeyInput.value);
});

updateGroqKeyPanel();


compressButton.addEventListener("click", async () => {
  const text = inputText.value.trim();
  const method = methodSelect.value;

  // Don't send an empty request.
  if (!text) {
    resultPlaceholder.textContent = "Please enter some text first.";
    resultOutput.textContent = "";
    compressionStats.textContent = "";
    return;
  }

  // Give the user feedback while the backend is working.
  compressButton.disabled = true;
  compressButton.textContent = "Compressing...";
  resultPlaceholder.textContent = "Working on your text...";
  resultOutput.textContent = "";
  compressionStats.textContent = "";

  try {
    
if (method === "ai-summary" && !groqApiKeyInput.value.trim()) {
  resultPlaceholder.textContent = "Please enter your Groq API key first.";
  return;
}

const headers = {
  "Content-Type": "application/json"
};

if (method === "ai-summary") {
  headers["X-Groq-API-Key"] = groqApiKeyInput.value.trim();
}

const response = await fetch("/api/compress", {
  method: "POST",
  headers: headers,
  body: JSON.stringify({
    text: text,
    method: method
  })
});


    // Read the server response.
    const responseBody = await response.text();

    let data;
    try {
      data = JSON.parse(responseBody);
    } catch {
      data = null;
    }

    if (!response.ok) {
      throw new Error(data?.error || responseBody || "The request failed.");
    }

    // Display the compressed text.
    resultPlaceholder.textContent = "";
    resultOutput.textContent = data.compressed_text;

    // Display the character counts and reduction percentage.
    compressionStats.textContent =
      `Original: ${data.original_chars} characters · ` +
      `Result: ${data.compressed_chars} characters · ` +
      `Reduction: ${data.reduction_percent.toFixed(1)}%`;
  } catch (error) {
    resultPlaceholder.textContent =
      `Couldn't compress the text: ${error.message}`;
    resultOutput.textContent = "";
    compressionStats.textContent = "";
  } finally {
    // Re-enable the button whether the request succeeded or failed.
    compressButton.disabled = false;
    compressButton.textContent = "Compress text";
  }
});
