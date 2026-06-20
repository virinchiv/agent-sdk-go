package conversation_test

import (
	"context"
	"testing"

	"github.com/agenticenv/agent-sdk-go/pkg/conversation"
	"github.com/agenticenv/agent-sdk-go/pkg/conversation/inmem"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestDefaultConfig(t *testing.T) {
	conv := inmem.NewInMemoryConversation()
	cfg := conversation.DefaultConfig(conv)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Size != conversation.DefaultSize {
		t.Fatalf("size = %d", cfg.Size)
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	cfg := (conversation.Config{Conversation: inmem.NewInMemoryConversation()}).WithDefaults()
	if cfg.Size != conversation.DefaultSize {
		t.Fatalf("size = %d", cfg.Size)
	}
}

func TestConfig_Validate_missingConversation(t *testing.T) {
	if err := (conversation.Config{}).Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestConfig_ListOptions(t *testing.T) {
	cfg := conversation.DefaultConfig(inmem.NewInMemoryConversation())
	opts := cfg.ListOptions()
	if len(opts) != 1 {
		t.Fatalf("opts len = %d", len(opts))
	}
}

func TestValidateDistributed_inmemRemoteWorkers(t *testing.T) {
	conv := inmem.NewInMemoryConversation()
	if err := conversation.ValidateDistributed(conv, true); err == nil {
		t.Fatal("expected distributed error")
	}
	if err := conversation.ValidateDistributed(conv, false); err != nil {
		t.Fatal(err)
	}
}

type distributedConv struct {
	interfaces.Conversation
}

func (distributedConv) IsDistributed() bool { return true }
func (distributedConv) AddMessage(context.Context, string, interfaces.Message) error {
	return nil
}
func (distributedConv) ListMessages(context.Context, string, ...interfaces.ListMessagesOption) ([]interfaces.Message, error) {
	return nil, nil
}
func (distributedConv) Clear(context.Context, string) error { return nil }

func TestValidateDistributed_distributedOK(t *testing.T) {
	if err := conversation.ValidateDistributed(distributedConv{}, true); err != nil {
		t.Fatal(err)
	}
}
