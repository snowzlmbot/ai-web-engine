package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/snowzlmbot/ai-web-engine/internal/crypto"
)

type Message struct {
	ID      string `json:"id,omitempty"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	CreatedAt        int64     `json:"createdAt"`
	UpdatedAt        int64     `json:"updatedAt"`
	ProviderID       string    `json:"providerId,omitempty"`
	ModelID          string    `json:"modelId,omitempty"`
	ReasoningLevel   string    `json:"reasoningLevel,omitempty"`
	ReasoningDisplay string    `json:"reasoningDisplay,omitempty"`
	Messages         []Message `json:"messages"`
}

const (
	ReasoningDisplayOff     = "off"
	ReasoningDisplayPartial = "partial"
	ReasoningDisplayAll     = "all"
)

func ValidReasoningDisplay(value string) bool {
	return value == ReasoningDisplayOff || value == ReasoningDisplayPartial || value == ReasoningDisplayAll
}

type Store struct {
	dir string
	key []byte
	mu  sync.Mutex
}

func NewStore(dir, keyPath string) (*Store, error) {
	key, err := crypto.EnsureKey(keyPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}
	return &Store{dir: dir, key: key}, nil
}

func (s *Store) List() ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	result := make([]Session, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			continue
		}
		data, err := s.readLocked(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read session %s: %w", entry.Name(), err)
		}
		var item Session
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("decode session %s: %w", entry.Name(), err)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result, nil
}

func (s *Store) Get(id string) (Session, error) {
	if !validID(id) {
		return Session{}, errors.New("invalid session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked(s.path(id))
	if err != nil {
		return Session{}, err
	}
	var item Session
	if err := json.Unmarshal(data, &item); err != nil {
		return Session{}, fmt.Errorf("decode session: %w", err)
	}
	return item, nil
}

func (s *Store) Create() (Session, error) {
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().Unix()
	item := Session{ID: id, CreatedAt: now, UpdatedAt: now, Messages: []Message{}}
	if err := s.Save(item); err != nil {
		return Session{}, err
	}
	return item, nil
}

func (s *Store) Save(item Session) error {
	if !validID(item.ID) {
		return errors.New("invalid session id")
	}
	if item.CreatedAt == 0 {
		item.CreatedAt = time.Now().Unix()
	}
	item.UpdatedAt = time.Now().Unix()
	if item.Title == "" {
		for _, message := range item.Messages {
			if message.Role == "user" {
				item.Title = firstRunes(message.Content, 20)
				break
			}
		}
	}
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return crypto.ProtectFile(s.key, s.path(item.ID), data)
}

func (s *Store) Delete(id string) error {
	if !validID(id) {
		return errors.New("invalid session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) readLocked(path string) ([]byte, error) {
	return crypto.UnprotectFile(s.key, path)
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".enc") }

func validID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if (i == 8 || i == 13 || i == 18 || i == 23) && c == '-' {
			continue
		}
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

func firstRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > max {
		return string(runes[:max])
	}
	return string(runes)
}

func newID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}
