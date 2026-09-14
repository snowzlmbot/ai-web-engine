package generation

import (
	"context"
	"testing"
	"time"
)

func TestTaskReplaysEventsAndTerminatesAfterDone(t *testing.T) {
	task := NewTask("task-1", "session-1", "message-1", "draft-1")
	task.Publish(Event{Type: "started"})
	task.Publish(Event{Type: "reasoning", Content: "draft"})
	task.Publish(Event{Type: "delta", Content: "answer"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream := task.Subscribe(ctx)
	var got []Event
	for event := range stream {
		got = append(got, event)
		if len(got) == 3 {
			break
		}
	}
	if len(got) != 3 || got[1].Content != "draft" || got[2].Content != "answer" {
		t.Fatalf("unexpected replay: %+v", got)
	}

	task.Publish(Event{Type: "done", Done: true})
	_, finished := task.Snapshot()
	if !finished || !task.IsFinished() {
		t.Fatal("task did not become terminal")
	}
	view := task.View()
	if view.SessionID != "session-1" || view.MessageID != "message-1" || len(view.Events) != 4 {
		t.Fatalf("unexpected task view: %+v", view)
	}
}

func TestManagerFindsOnlyActiveTaskForSession(t *testing.T) {
	manager := NewManager()
	finished := NewTask("finished", "session-1", "message-1", "draft-1")
	finished.Publish(Event{Type: "done", Done: true})
	manager.Add(finished)
	active := NewTask("active", "session-1", "message-2", "draft-2")
	manager.Add(active)
	other := NewTask("other", "session-2", "message-3", "draft-3")
	manager.Add(other)

	got, ok := manager.ActiveForSession("session-1")
	if !ok || got.ID != "active" {
		t.Fatalf("active task = %v, ok=%v", got, ok)
	}
	if _, ok := manager.ActiveForSession("missing"); ok {
		t.Fatal("found task for missing session")
	}
}
