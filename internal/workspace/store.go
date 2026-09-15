package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	RootLogs   = "logs"
	RootSkills = "skills"
	RootLocal  = "This machine skills"
)

var (
	ErrUnknownRoot      = errors.New("workspace root is not allowed")
	ErrUnsafePath       = errors.New("workspace path is unsafe")
	ErrOfficialReadOnly = errors.New("official skills root is read-only")
	ErrIndexReadOnly    = errors.New("skills index is generated automatically")
)

type Entry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

type Store struct {
	mu    sync.RWMutex
	roots map[string]string
}

func New(officialSkills, localSkills, logs string) *Store {
	return &Store{roots: map[string]string{
		RootSkills: officialSkills,
		RootLocal:  localSkills,
		RootLogs:   logs,
	}}
}

func (s *Store) Roots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]string, 0, len(s.roots))
	for root := range s.roots {
		result = append(result, root)
	}
	sort.Strings(result)
	return result
}

// Read is retained for small callers and tests. API handlers should use
// StreamRead so file size never becomes an in-memory rejection condition.
func (s *Store) Read(root, rel string) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := s.StreamRead(root, rel, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// StreamRead copies a permitted regular file directly to dst. It has no file
// size or text-format limit; callers decide how much context to retain.
func (s *Store) StreamRead(root, rel string, dst io.Writer) (int64, error) {
	path, err := s.resolve(root, rel, false)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("workspace path is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return io.Copy(dst, file)
}

func (s *Store) List(root, rel string) ([]Entry, error) {
	s.mu.RLock()
	rootPath, ok := s.roots[root]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownRoot
	}
	var path string
	var err error
	if rel == "" || rel == "." {
		path = rootPath
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, ErrUnsafePath
		}
	} else {
		path, err = s.resolve(root, rel, false)
		if err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path is not a directory")
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.Type()&os.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(path, item.Name())
		childInfo, statErr := item.Info()
		if statErr != nil || childInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		relPath, relErr := filepath.Rel(rootPath, child)
		if relErr != nil {
			continue
		}
		entry := Entry{Path: filepath.ToSlash(relPath), Directory: childInfo.IsDir()}
		if !entry.Directory {
			entry.SizeBytes = childInfo.Size()
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// Write replaces a permitted file atomically without imposing a size or text
// encoding limit. Use WriteStream for large payloads.
func (s *Store) Write(root, rel string, data []byte) error {
	return s.WriteStream(root, rel, bytes.NewReader(data))
}

func (s *Store) WriteStream(root, rel string, src io.Reader) error {
	if root == RootSkills {
		return ErrOfficialReadOnly
	}
	if root == RootLocal && filepath.ToSlash(rel) == "skills-index.json" {
		return ErrIndexReadOnly
	}
	path, err := s.prepareWritePath(root, rel)
	if err != nil {
		return err
	}
	return writeAtomicStream(path, src, 0o600)
}

func (s *Store) Append(root, rel string, data []byte) error {
	return s.AppendStream(root, rel, bytes.NewReader(data))
}

func (s *Store) AppendStream(root, rel string, src io.Reader) error {
	if root != RootLogs {
		return fmt.Errorf("append is allowed only for logs")
	}
	path, err := s.prepareWritePath(root, rel)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	_, err = io.Copy(file, src)
	return err
}

func (s *Store) prepareWritePath(root, rel string) (string, error) {
	s.mu.RLock()
	rootPath, ok := s.roots[root]
	s.mu.RUnlock()
	if !ok {
		return "", ErrUnknownRoot
	}
	if strings.TrimSpace(rootPath) == "" {
		return "", ErrUnknownRoot
	}
	if root == RootSkills {
		return "", ErrOfficialReadOnly
	}
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return "", err
	}
	return s.resolve(root, rel, true)
}

func (s *Store) resolve(root, rel string, forWrite bool) (string, error) {
	s.mu.RLock()
	rootPath, ok := s.roots[root]
	s.mu.RUnlock()
	if !ok {
		return "", ErrUnknownRoot
	}
	if strings.TrimSpace(rootPath) == "" || strings.IndexByte(rel, 0) >= 0 || strings.Contains(rel, "\\") {
		return "", ErrUnsafePath
	}
	if rel == "" || pathpkg.IsAbs(rel) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", ErrUnsafePath
	}
	clean := pathpkg.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrUnsafePath
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." || part == "" {
			return "", ErrUnsafePath
		}
	}
	rootInfo, err := os.Lstat(rootPath)
	if err != nil {
		if forWrite && errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(rootPath, 0o700); err != nil {
				return "", err
			}
			rootInfo, err = os.Lstat(rootPath)
		}
		if err != nil {
			return "", err
		}
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", ErrUnsafePath
	}
	current := rootPath
	parts := strings.Split(clean, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) && forWrite && index < len(parts)-1 {
				if mkdirErr := os.MkdirAll(current, 0o700); mkdirErr != nil {
					return "", mkdirErr
				}
				info, statErr = os.Lstat(current)
			}
			if errors.Is(statErr, os.ErrNotExist) && forWrite && index == len(parts)-1 {
				return current, nil
			}
			if statErr != nil {
				return "", statErr
			}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", ErrUnsafePath
		}
	}
	return current, nil
}

func writeAtomicStream(path string, src io.Reader, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".workspace-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
