package generation

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	DraftID   string `json:"draftId,omitempty"`
	Content   string `json:"content,omitempty"`
	Message   string `json:"message,omitempty"`
	Done      bool   `json:"done,omitempty"`
}

type Task struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	MessageID string    `json:"messageId"`
	DraftID   string    `json:"draftId"`
	CreatedAt time.Time `json:"createdAt"`

	mu       sync.RWMutex
	events   []Event
	finished bool
	waiters  map[chan Event]struct{}
}

func NewTask(id, sessionID, messageID, draftID string) *Task {
	return &Task{
		ID: id, SessionID: sessionID, MessageID: messageID, DraftID: draftID,
		CreatedAt: time.Now(), waiters: map[chan Event]struct{}{},
	}
}

func (t *Task) Publish(event Event) {
	if event.SessionID == "" {
		event.SessionID = t.SessionID
	}
	if event.MessageID == "" {
		event.MessageID = t.MessageID
	}
	if event.DraftID == "" {
		event.DraftID = t.DraftID
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.events = append(t.events, event)
	terminal := event.Done || event.Type == "error"
	for waiter := range t.waiters {
		select {
		case waiter <- event:
		default:
		}
	}
	if terminal {
		t.finished = true
		for waiter := range t.waiters {
			close(waiter)
		}
		t.waiters = map[chan Event]struct{}{}
	}
}

func (t *Task) Snapshot() ([]Event, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Event(nil), t.events...), t.finished
}

func (t *Task) Subscribe(ctx context.Context) <-chan Event {
	t.mu.Lock()
	events := append([]Event(nil), t.events...)
	finished := t.finished
	ch := make(chan Event, len(events)+64)
	for _, event := range events {
		ch <- event
	}
	if !finished {
		t.waiters[ch] = struct{}{}
	}
	t.mu.Unlock()
	if finished {
		close(ch)
	}

	go func() {
		<-ctx.Done()
		t.mu.Lock()
		if _, ok := t.waiters[ch]; ok {
			delete(t.waiters, ch)
			close(ch)
		}
		t.mu.Unlock()
	}()
	return ch
}

func (t *Task) IsFinished() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.finished
}

type TaskView struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	MessageID string    `json:"messageId"`
	DraftID   string    `json:"draftId"`
	CreatedAt time.Time `json:"createdAt"`
	Events    []Event   `json:"events"`
	Finished  bool      `json:"finished"`
}

func (t *Task) View() TaskView {
	events, finished := t.Snapshot()
	return TaskView{ID: t.ID, SessionID: t.SessionID, MessageID: t.MessageID, DraftID: t.DraftID, CreatedAt: t.CreatedAt, Events: events, Finished: finished}
}

var ErrTaskNotFound = errors.New("generation task not found")

type Manager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

func NewManager() *Manager { return &Manager{tasks: map[string]*Task{}} }

func (m *Manager) Add(task *Task) {
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()
}

func (m *Manager) Get(id string) (*Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task := m.tasks[id]
	if task == nil {
		return nil, ErrTaskNotFound
	}
	return task, nil
}

func (m *Manager) ActiveForSession(sessionID string) (*Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var latest *Task
	for _, task := range m.tasks {
		if task.SessionID != sessionID || task.IsFinished() {
			continue
		}
		if latest == nil || task.CreatedAt.After(latest.CreatedAt) {
			latest = task
		}
	}
	return latest, latest != nil
}
