package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

var (
	a2aToolNameTemplate        = "a2a_%s_%s"
	a2aToolDisplayNameTemplate = "%s A2A %s Tool"
)

// defaultA2AToolTimeout is used in [a2aConfigFingerprint] when [A2AConfig.Timeout] is zero.
// Uses [types.DefaultA2ATimeout] so the fingerprint matches the value applied in [pkg/a2a/client.BuildConfig].
const defaultA2AToolTimeout = types.DefaultA2ATimeout

var _ interfaces.Tool = (*A2ATool)(nil)

// NOTE: A2ATools for the same server share one A2AClient. The default pkg/a2a/client is safe
// for concurrent use; custom A2AClient implementations should document concurrency behaviour.

// A2ATool implements [interfaces.Tool] for one A2A skill on a remote agent server.
// Execute packages the LLM-supplied args as a JSON text message, calls [interfaces.A2AClient.SendMessage],
// then extracts the reply text from the returned message (or serialises a task to JSON if the
// server returns a task handle instead of a synchronous message).
type A2ATool struct {
	// ServerName is the stable key identifying the A2A server (used in tool name / display name).
	ServerName string
	// Spec carries the skill ID (Spec.Name), human-readable description, and optional parameter schema.
	Spec interfaces.ToolSpec
	// Client is the A2A client used to invoke the remote agent.
	Client interfaces.A2AClient
}

// a2aToolName returns the registered tool name for an A2A server key and skill ID.
// Trims whitespace on both inputs; returns "" if either is empty after trim.
func a2aToolName(serverName, skillID string) string {
	sn := strings.TrimSpace(serverName)
	sid := strings.TrimSpace(skillID)
	if sn == "" || sid == "" {
		return ""
	}
	return fmt.Sprintf(a2aToolNameTemplate, sn, sid)
}

// a2aToolDisplayName returns the display name for an A2A server key and skill ID.
// Trims whitespace on both inputs; returns "" if either is empty after trim.
func a2aToolDisplayName(serverName, skillID string) string {
	sn := strings.TrimSpace(serverName)
	sid := strings.TrimSpace(skillID)
	if sn == "" || sid == "" {
		return ""
	}
	return fmt.Sprintf(a2aToolDisplayNameTemplate, sn, sid)
}

// NewA2ATool builds an A2ATool from a server key, skill spec, and client.
// When spec.Parameters is nil, [A2ATool.Parameters] returns a default {"type":"object"} schema.
func NewA2ATool(serverName string, spec interfaces.ToolSpec, client interfaces.A2AClient) *A2ATool {
	return &A2ATool{ServerName: serverName, Spec: spec, Client: client}
}

// Name implements [interfaces.Tool].
func (m *A2ATool) Name() string {
	if m == nil {
		return ""
	}
	return a2aToolName(m.ServerName, m.Spec.Name)
}

// DisplayName implements [interfaces.Tool].
func (m *A2ATool) DisplayName() string {
	if m == nil {
		return ""
	}
	return a2aToolDisplayName(m.ServerName, m.Spec.Name)
}

// Description implements [interfaces.Tool].
func (m *A2ATool) Description() string {
	if m == nil {
		return ""
	}
	return m.Spec.Description
}

// Parameters implements [interfaces.Tool]. Returns a default object schema when spec parameters are nil.
func (m *A2ATool) Parameters() interfaces.JSONSchema {
	if m == nil || m.Spec.Parameters == nil {
		return interfaces.JSONSchema{"type": "object"}
	}
	return m.Spec.Parameters
}

// Execute implements [interfaces.Tool].
//
// The LLM-supplied args map is marshalled to a JSON string and sent as a single "text" part in a
// user message. The remote agent's reply is handled as follows:
//   - Message result: all text parts are collected and joined with newlines.
//   - Task result (async): the task is JSON-encoded and returned as a string.
//     Callers that need full task lifecycle management should use [interfaces.A2AClient] directly.
//   - Empty result (neither message nor task): an empty string is returned without error.
func (m *A2ATool) Execute(ctx context.Context, args map[string]any) (any, error) {
	if m == nil || m.Client == nil {
		return nil, fmt.Errorf("a2a tool: nil client")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("a2a tool: marshal args: %w", err)
	}
	result, err := m.Client.SendMessage(ctx, interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: string(raw)}},
		},
	})
	if err != nil {
		return nil, err
	}
	if result.Message != nil {
		return a2aCollectText(result.Message), nil
	}
	if result.Task != nil {
		b, err := json.Marshal(result.Task)
		if err != nil {
			return fmt.Sprintf("task:%s status:%s", result.Task.ID, result.Task.Status), nil
		}
		return string(b), nil
	}
	return "", nil
}

// a2aCollectText concatenates the text from all text-kind parts in msg, separated by newlines.
// Non-text parts and empty text values are silently skipped.
func a2aCollectText(msg *interfaces.A2AMessage) string {
	if msg == nil || len(msg.Parts) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range msg.Parts {
		if p.Kind != "text" || p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

// a2aConfigFingerprint returns a stable SHA-256 digest of the A2A wiring used by
// [agentConfig.agentConfigFingerprint]: server keys, base URLs, auth type (presence only —
// token values are never included), extra HTTP header keys, timeouts, skill filters, and sorted
// extra A2A client names (from WithA2AClients).
// Returns an empty string when there are no servers and no extra client names.
func a2aConfigFingerprint(servers A2AServers, extraClientNames []string) string {
	if len(servers) == 0 && len(extraClientNames) == 0 {
		return ""
	}
	shot := a2aFpShot{}
	keys := make([]string, 0, len(servers))
	for k := range servers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		cfg := servers[k]
		to := cfg.Timeout
		if to == 0 {
			to = defaultA2AToolTimeout
		}
		authLabel := "none"
		if strings.TrimSpace(cfg.Token) != "" {
			authLabel = "bearer_token"
		}
		var hdrKeys []string
		for hk := range cfg.Headers {
			if s := strings.TrimSpace(hk); s != "" {
				hdrKeys = append(hdrKeys, s)
			}
		}
		sort.Strings(hdrKeys)

		row := a2aFpServerRow{
			Key:             k,
			URL:             strings.TrimSpace(cfg.URL),
			AuthType:        authLabel,
			HasBearerToken:  strings.TrimSpace(cfg.Token) != "",
			ExtraHeaderKeys: hdrKeys,
			TimeoutNs:       to.Nanoseconds(),
			SkipTLSVerify:   cfg.SkipTLSVerify,
		}
		if len(cfg.SkillFilter.AllowSkills) > 0 {
			row.AllowSkills = append([]string(nil), cfg.SkillFilter.AllowSkills...)
			sort.Strings(row.AllowSkills)
		}
		if len(cfg.SkillFilter.BlockSkills) > 0 {
			row.BlockSkills = append([]string(nil), cfg.SkillFilter.BlockSkills...)
			sort.Strings(row.BlockSkills)
		}
		shot.Servers = append(shot.Servers, row)
	}

	names := append([]string(nil), extraClientNames...)
	sort.Strings(names)
	shot.ExtraClients = names

	b, err := json.Marshal(shot)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// a2aExtraClientNames returns sorted, non-empty A2A client names from WithA2AClients
// for use in [a2aConfigFingerprint].
func a2aExtraClientNames(clients []interfaces.A2AClient) []string {
	var out []string
	for _, cl := range clients {
		if cl == nil {
			continue
		}
		if n := strings.TrimSpace(cl.Name()); n != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Fingerprint payload types (unexported; JSON-serialised for SHA-256)
// ---------------------------------------------------------------------------

type a2aFpShot struct {
	Servers      []a2aFpServerRow `json:"servers,omitempty"`
	ExtraClients []string         `json:"extra_clients,omitempty"`
}

type a2aFpServerRow struct {
	Key             string   `json:"key"`
	URL             string   `json:"url"`
	AuthType        string   `json:"auth_type,omitempty"`
	HasBearerToken  bool     `json:"has_bearer,omitempty"`
	ExtraHeaderKeys []string `json:"header_keys,omitempty"`
	TimeoutNs       int64    `json:"timeout_ns"`
	SkipTLSVerify   bool     `json:"skip_tls,omitempty"`
	AllowSkills     []string `json:"allow_skills,omitempty"`
	BlockSkills     []string `json:"block_skills,omitempty"`
}
