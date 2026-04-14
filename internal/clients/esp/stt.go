package esp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

type transcribeResult struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

// transcribe sends a WAV file to the whisper server and returns the
// detected text.
func transcribe(ctx context.Context, client *http.Client, whisperURL string, wav []byte) (string, error) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	fw, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(wav); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}
	if err := w.WriteField("language", "en"); err != nil {
		return "", fmt.Errorf("write language: %w", err)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, whisperURL, body)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, b)
	}

	var result transcribeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Text, nil
}
