package setup

import (
	"testing"

	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

func TestParseMemoryStoreMode(t *testing.T) {
	t.Parallel()

	mode, err := ParseMemoryStoreMode("always")
	if err != nil || mode != memory.StoreModeAlways {
		t.Fatalf("ParseMemoryStoreMode(always) = %q, %v", mode, err)
	}

	mode, err = ParseMemoryStoreMode("")
	if err != nil || mode != memory.StoreModeOnDemand {
		t.Fatalf("ParseMemoryStoreMode(empty) = %q, %v", mode, err)
	}

	if _, err := ParseMemoryStoreMode("invalid"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
