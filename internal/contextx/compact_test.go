package contextx

import (
	"fmt"
	"strings"
	"testing"

	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

type readerFunc func(root, rel string, dst interface{ Write([]byte) (int, error) }) (int64, error)

func TestCompactPreservesConstraintsAndCurrentRequest(t *testing.T) {
	messages := []session.Message{{ID: "system", Role: "system", Content: "official constraints"}}
	for i := 0; i < 20; i++ {
		messages = append(messages, session.Message{ID: string(rune(i + 1)), Role: "user", Content: fmt.Sprintf("old context %d %s", i, strings.Repeat("old context ", 40))})
	}
	messages = append(messages, session.Message{ID: "current", Role: "user", Content: "current request"})
	result := Compact(messages, Options{MaxMessages: 4, MaxMessageRunes: 1000})
	if !result.Compacted {
		t.Fatalf("compaction did not run: %+v", result)
	}
	var hasSummary, hasConstraints, hasCurrent bool
	for _, message := range result.Messages {
		hasSummary = hasSummary || strings.Contains(message.Content, "[context-summary]")
		hasConstraints = hasConstraints || strings.Contains(message.Content, "official constraints")
		hasCurrent = hasCurrent || strings.Contains(message.Content, "current request")
	}
	if !hasSummary || !hasConstraints || !hasCurrent {
		t.Fatalf("compaction lost protected content: %+v", result.Messages)
	}
}

func TestDedupeRetainsRoleDistinctMessages(t *testing.T) {
	result := Compact([]session.Message{
		{Role: "user", Content: "same"},
		{Role: "assistant", Content: "same"},
		{Role: "user", Content: "same"},
	}, Options{MaxMessages: 10, MaxMessageRunes: 1000})
	if len(result.Messages) != 2 || result.Messages[0].Role != "user" || result.Messages[1].Role != "assistant" {
		t.Fatalf("unexpected messages: %+v", result.Messages)
	}
}
