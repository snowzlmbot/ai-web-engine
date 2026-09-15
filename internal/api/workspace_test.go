package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
)

func TestWorkspaceAPIStreamsNestedReadWriteAndProtectsOfficialSkills(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "shortx-rule-creator")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# official"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithRoot(t, root, "", config.Default())
	handler := server.Handler()

	roots := httptest.NewRecorder()
	handler.ServeHTTP(roots, httptest.NewRequest(http.MethodGet, "/api/workspace/roots", nil))
	if roots.Code != http.StatusOK {
		t.Fatalf("roots status=%d body=%s", roots.Code, roots.Body.String())
	}
	var rootPayload struct {
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal(roots.Body.Bytes(), &rootPayload); err != nil {
		t.Fatal(err)
	}
	if len(rootPayload.Roots) != 3 {
		t.Fatalf("roots=%v", rootPayload.Roots)
	}

	writeURL := "/api/workspace/This%20machine%20skills/team/deep/reference.md"
	write := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, writeURL, strings.NewReader("large enough reference content"))
	handler.ServeHTTP(write, request)
	if write.Code != http.StatusCreated {
		t.Fatalf("write status=%d body=%s", write.Code, write.Body.String())
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, writeURL, nil))
	if read.Code != http.StatusOK || read.Body.String() != "large enough reference content" {
		t.Fatalf("read status=%d body=%q", read.Code, read.Body.String())
	}
	if _, err := io.ReadAll(strings.NewReader(read.Body.String())); err != nil {
		t.Fatal(err)
	}

	official := httptest.NewRecorder()
	handler.ServeHTTP(official, httptest.NewRequest(http.MethodPut, "/api/workspace/skills/new/SKILL.md", strings.NewReader("no")))
	if official.Code != http.StatusForbidden {
		t.Fatalf("official write status=%d body=%s", official.Code, official.Body.String())
	}
	indexData, err := os.ReadFile(filepath.Join(root, "This machine skills", "skills-index.json"))
	if err != nil || !strings.Contains(string(indexData), `"format": 1`) {
		t.Fatalf("startup index missing or invalid: err=%v data=%q", err, indexData)
	}
}

func TestWorkspaceAPIAppendsOnlyToLogs(t *testing.T) {
	root := t.TempDir()
	server := newTestServerWithRoot(t, root, "", config.Default())
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspace/logs/deep/engine.log", strings.NewReader("entry\n")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("append status=%d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "logs", "deep", "engine.log"))
	if err != nil || string(data) != "entry\n" {
		t.Fatalf("log=%q err=%v", data, err)
	}
	local := httptest.NewRecorder()
	server.Handler().ServeHTTP(local, httptest.NewRequest(http.MethodPost, "/api/workspace/This%20machine%20skills/x.txt", strings.NewReader("no")))
	if local.Code == http.StatusCreated {
		t.Fatal("append outside logs was accepted")
	}
}
