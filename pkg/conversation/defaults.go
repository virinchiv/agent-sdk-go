package conversation

import (
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// DefaultSize is the default max messages fetched for LLM context.
const DefaultSize = 20

// DefaultConfig returns a [Config] with SDK defaults for size and save behavior.
func DefaultConfig(conv interfaces.Conversation) Config {
	return Config{
		Conversation: conv,
		Size:         DefaultSize,
	}
}
