package common

import (
	"fmt"
	"os"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// ValidateEmbeddingConfig ensures pgvector can call an OpenAI-compatible embeddings API.
// When LLM_PROVIDER is not openai, EMBEDDING_APIKEY (or OPENAI_APIKEY) must be set explicitly.
func ValidateEmbeddingConfig(provider interfaces.LLMProvider, settings *Settings) error {
	if settings == nil {
		return fmt.Errorf("settings is nil")
	}
	if settings.EmbeddingAPIKey == "" {
		return fmt.Errorf("EMBEDDING_APIKEY or LLM_APIKEY is required for pgvector embeddings")
	}
	explicit := strings.TrimSpace(os.Getenv("EMBEDDING_APIKEY")) != "" ||
		strings.TrimSpace(os.Getenv("OPENAI_APIKEY")) != ""
	if explicit {
		return nil
	}
	switch provider {
	case interfaces.LLMProviderOpenAI, "":
		return nil
	default:
		return fmt.Errorf(
			"pgvector embeddings need an OpenAI-compatible API key in EMBEDDING_APIKEY (or OPENAI_APIKEY); "+
				"LLM_PROVIDER=%s cannot use LLM_APIKEY for /embeddings",
			provider,
		)
	}
}
