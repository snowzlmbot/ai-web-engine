package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamReadWriteAllowsLargeNestedFilesInPermittedRoots(t *testing.T) {
	root := t.TempDir()
	official := filepath.Join(root, "skills")
	local := filepath.Join(root, "This machine skills")
	logs := filepath.Join(root, "logs")
	store := New(official, local, logs)
	payload := bytes.Repeat([]byte("a"), 3<<20)
	path := "a/b/c/large.txt"
	if err := store.WriteStream(RootLocal, path, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if size, err := store.StreamRead(RootLocal, path, &got); err != nil {
		t.Fatal(err)
	} else if size != int64(len(payload)) {
		t.Fatalf("stream size=%d, want %d", size, len(payload))
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatal("stream read changed large payload")
	}
}

func TestWorkspaceRootsAndWritePolicy(t *testing.T) {
	root := t.TempDir()
	store := New(filepath.Join(root, "skills"), filepath.Join(root, "This machine skills"), filepath.Join(root, "logs"))
	if got := store.Roots(); len(got) != 3 || got[0] != RootLocal || got[1] != RootLogs || got[2] != RootSkills {
		t.Fatalf("roots=%v", got)
	}
	if err := store.Write(RootSkills, "new/SKILL.md", []byte("must reject")); !errors.Is(err, ErrOfficialReadOnly) {
		t.Fatalf("official write error=%v", err)
	}
	if err := store.WriteStream(RootLocal, "skills-index.json", strings.NewReader("must reject")); !errors.Is(err, ErrIndexReadOnly) {
		t.Fatalf("index write error=%v", err)
	}
	if err := store.Append(RootLogs, "nested/engine.log", []byte("line\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "logs", "nested", "engine.log"))
	if err != nil || string(data) != "line\n" {
		t.Fatalf("log data=%q err=%v", data, err)
	}
}

func TestWorkspaceRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store := New(filepath.Join(root, "skills"), filepath.Join(root, "This machine skills"), filepath.Join(root, "logs"))
	if err := store.WriteStream(RootLocal, "../escape.txt", strings.NewReader("x")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("traversal error=%v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "This machine skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "This machine skills", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStream(RootLocal, "escape/file.txt", strings.NewReader("x")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink error=%v", err)
	}
}
