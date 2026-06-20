package common

import "fmt"

// ValidateEmbeddingConfig ensures pgvector can call an OpenAI-compatible embeddings API.
func ValidateEmbeddingConfig(settings *Settings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}
	if settings.EmbeddingAPIKey == "" {
		return fmt.Errorf("EMBEDDING_OPENAI_APIKEY is required for pgvector memory embeddings (OpenAI-compatible; separate from LLM_APIKEY)")
	}
	return nil
}
