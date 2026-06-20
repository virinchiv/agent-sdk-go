package common

import (
	"testing"

	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

func TestParseStoreMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want memory.StoreMode
	}{
		{"", memory.StoreModeOnDemand},
		{"ondemand", memory.StoreModeOnDemand},
		{"on-demand", memory.StoreModeOnDemand},
		{"always", memory.StoreModeAlways},
	}

	for _, tt := range tests {
		got, err := ParseStoreMode(tt.raw)
		if err != nil {
			t.Fatalf("ParseStoreMode(%q): %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("ParseStoreMode(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}

	if _, err := ParseStoreMode("invalid"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
