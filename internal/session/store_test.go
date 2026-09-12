package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "sessions"), filepath.Join(dir, "config", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	item.Messages = append(item.Messages, Message{Role: "user", Content: "hello"}, Message{Role: "assistant", Content: "world"})
	if err := store.Save(item); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(item.ID)
	if err != nil || len(loaded.Messages) != 2 {
		t.Fatalf("load failed: %+v %v", loaded, err)
	}
	if loaded.Title != "hello" {
		t.Fatalf("title = %q", loaded.Title)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "sessions", item.ID+".enc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) == "" || string(contents) == "hello" || string(contents) == "world" {
		t.Fatal("session appears to be plaintext")
	}
}
