package main

import "testing"

func TestShouldSkipCopilotKitEmptyDeltaContent(t *testing.T) {
	if !shouldSkipCopilotKitEmptyDeltaContent([]byte(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"x","delta":""}`)) {
		t.Fatal("want skip empty TEXT_MESSAGE_CONTENT")
	}
	if !shouldSkipCopilotKitEmptyDeltaContent([]byte(`{"type":"THINKING_TEXT_MESSAGE_CONTENT","messageId":"x","delta":""}`)) {
		t.Fatal("want skip empty THINKING_TEXT_MESSAGE_CONTENT")
	}
	if shouldSkipCopilotKitEmptyDeltaContent([]byte(`{"type":"TEXT_MESSAGE_CONTENT","delta":"hi"}`)) {
		t.Fatal("must not skip non-empty delta")
	}
	if shouldSkipCopilotKitEmptyDeltaContent([]byte(`{"type":"RUN_STARTED"}`)) {
		t.Fatal("must not skip non-content events")
	}
}
