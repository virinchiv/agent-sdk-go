package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pgmem "github.com/agenticenv/agent-sdk-go/pkg/memory/pgvector"
)

// OpenAIEmbedFunc returns a [pgmem.EmbedFunc] that calls an OpenAI-compatible embeddings API.
func OpenAIEmbedFunc(settings *Settings) (pgmem.EmbedFunc, error) {
	if settings == nil {
		return nil, fmt.Errorf("embed: settings is nil")
	}
	if settings.EmbeddingAPIKey == "" {
		return nil, fmt.Errorf("embed: EMBEDDING_OPENAI_APIKEY is required for pgvector")
	}
	model := strings.TrimSpace(settings.EmbeddingModel)
	if model == "" {
		return nil, fmt.Errorf("embed: EMBEDDING_OPENAI_MODEL is required")
	}
	base := strings.TrimRight(strings.TrimSpace(settings.EmbeddingBaseURL), "/")
	client := &http.Client{Timeout: 60 * time.Second}

	return func(ctx context.Context, text string) ([]float32, error) {
		body, err := json.Marshal(map[string]any{
			"input": text,
			"model": model,
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+settings.EmbeddingAPIKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("embeddings API %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		}

		var parsed struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, err
		}
		if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
			return nil, fmt.Errorf("embeddings API returned no vectors")
		}
		out := make([]float32, len(parsed.Data[0].Embedding))
		for i, v := range parsed.Data[0].Embedding {
			out[i] = float32(v)
		}
		return out, nil
	}, nil
}
