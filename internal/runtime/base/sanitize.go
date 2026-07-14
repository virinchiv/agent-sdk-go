package base

import (
	"regexp"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// Patterns for volatile tokens that hurt prompt-prefix cache stability when injected into content.
var (
	sanitizeTimestampRe = regexp.MustCompile(
		`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b` +
			`|\b(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun)\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\s+\d{4}\b`,
	)
	sanitizeSessionIDRe = regexp.MustCompile(
		`(?i)\b(?:session[_-]?id|conversation[_-]?id|run[_-]?id|request[_-]?id)\s*[:=]\s*["']?[A-Za-z0-9_-]+["']?` +
			`|\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`,
	)
	sanitizeWhitespaceRe = regexp.MustCompile(`[ \t]+`)
)

// sanitizeMessages returns a new message slice with volatile content stripped for LLM calls.
// The input slice is not modified (conversation stores stay unchanged).
func sanitizeMessages(msgs []interfaces.Message) []interfaces.Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]interfaces.Message, len(msgs))
	for i, msg := range msgs {
		msg.Content = sanitizeContent(msg.Content)
		out[i] = msg
	}
	return out
}

func sanitizeContent(s string) string {
	if s == "" {
		return s
	}
	s = sanitizeTimestampRe.ReplaceAllString(s, "")
	s = sanitizeSessionIDRe.ReplaceAllString(s, "")
	s = sanitizeWhitespaceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Collapse blank lines introduced by removals.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
