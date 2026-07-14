package base

import (
	"testing"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/stretchr/testify/require"
)

func TestSanitizeMessages_stripsVolatileContent(t *testing.T) {
	in := []interfaces.Message{{
		Role:    interfaces.MessageRoleUser,
		Content: "Hello  world  at 2024-06-01T12:00:00Z session_id=abc-123 and uuid 550e8400-e29b-41d4-a716-446655440000",
	}}
	out := sanitizeMessages(in)
	require.Len(t, out, 1)
	require.NotContains(t, out[0].Content, "2024-06-01")
	require.NotContains(t, out[0].Content, "session_id")
	require.NotContains(t, out[0].Content, "550e8400")
	require.Contains(t, out[0].Content, "Hello world")
	// Input unchanged.
	require.Contains(t, in[0].Content, "2024-06-01")
}

func TestSanitizeMessages_empty(t *testing.T) {
	require.Nil(t, sanitizeMessages(nil))
	require.Empty(t, sanitizeMessages([]interfaces.Message{}))
}
