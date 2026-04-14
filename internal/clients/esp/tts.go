package esp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type synthesizeRequest struct {
	Message    string `json:"message"`
	Name       string `json:"name,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

// synthesize calls the Piper voice server and returns WAV bytes.
func synthesize(ctx context.Context, client *http.Client, piperURL, text, voice string, sampleRate int) ([]byte, error) {
	reqBody := synthesizeRequest{
		Message:    text,
		Name:       voice,
		SampleRate: sampleRate,
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, piperURL, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("synthesize request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("synthesize returned %d: %s", resp.StatusCode, b)
	}
	return io.ReadAll(resp.Body)
}
