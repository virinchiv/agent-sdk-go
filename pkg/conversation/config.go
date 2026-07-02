package conversation

import (
	"errors"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// Config wires conversation history for the agent SDK.
type Config struct {
	// Conversation is the conversation backend implementation. Required.
	Conversation interfaces.Conversation

	// Size is the maximum number of messages to fetch for LLM context.
	// Zero or negative defaults to [DefaultSize].
	Size int

	// SaveOnIteration persists messages after each tool round instead of batching at run end.
	SaveOnIteration bool
}

// WithDefaults fills zero fields with SDK defaults. Conversation must be set separately.
func (c Config) WithDefaults() Config {
	if c.Size <= 0 {
		c.Size = DefaultSize
	}
	return c
}

// Validate checks the config. Call [WithDefaults] first.
func (c Config) Validate() error {
	if c.Conversation == nil {
		return errors.New("conversation config: Conversation is required")
	}
	return nil
}

// ValidateDistributed returns an error when conv is not distributed but remote workers require it.
func ValidateDistributed(conv interfaces.Conversation, remoteWorkers bool) error {
	if remoteWorkers && conv != nil && !conv.IsDistributed() {
		return errors.New("in-memory conversation cannot be used with remote workers: use distributed storage such as redis.NewConversation")
	}
	return nil
}

// ListOptions builds [interfaces.ListMessagesOption] values from this config.
func (c Config) ListOptions() []interfaces.ListMessagesOption {
	return []interfaces.ListMessagesOption{
		interfaces.WithLimit(c.Size),
	}
}
