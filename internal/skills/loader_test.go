package skills

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOptionalSupportsDirectoryZipAndSkillFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "folder")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: folder-skill\ndescription: 用于本机备份和安全回退的技能。\n---\n# folder skill\nUse folder rules."), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "bundle.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("zip-skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("---\nname: zip-skill\ndescription: >-\n  用于读取压缩包内的本机扩展技能，\n  仅在匹配请求时按需加载。\n---\n# zip skill\nUse zip rules.")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	items, err := LoadOptional(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("optional skills = %d, want 2: %+v", len(items), items)
	}
	if items[0].Source != "local" || items[1].Source != "local-zip" {
		t.Fatalf("unexpected sources: %+v", items)
	}
	if items[0].Summary != "用于本机备份和安全回退的技能。" || !strings.Contains(items[1].Summary, "用于读取压缩包内的本机扩展技能") || !strings.Contains(items[1].Summary, "仅在匹配请求时按需加载") {
		t.Fatalf("frontmatter descriptions were not summarized: %+v", items)
	}
}

func TestLoadOptionalMissingDirectoryIsValid(t *testing.T) {
	items, err := LoadOptional(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("missing optional directory returned %d skills", len(items))
	}
}

func TestReadZipSkillsRejectsZipSlipAndOversizedArchiveEntries(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "unsafe.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range []string{"../escape/SKILL.md", "safe/SKILL.md"} {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte("# test")); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	items, err := readZipSkills(archive)
	if err == nil {
		t.Fatal("unsafe archive was accepted")
	}
	if len(items) != 0 {
		t.Fatalf("unsafe archive returned skills: %+v", items)
	}
}

func TestSelectLocalOnlyLoadsMatchingExtensions(t *testing.T) {
	items := []Skill{
		{Name: "backup/SKILL.md", Summary: "backup and rollback", Text: "backup"},
		{Name: "weather/SKILL.md", Summary: "weather", Text: "weather"},
	}
	selected := SelectLocal(items, "please generate a rollback backup instruction", 3)
	if len(selected) != 1 || selected[0].Name != "backup/SKILL.md" {
		t.Fatalf("selected local skills = %+v", selected)
	}
}

func TestRefreshOptionalIndexWritesMetadataOnlyAndUpdates(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# rollback\nThis body must stay out of the index."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := RefreshOptionalIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("loaded skills = %d, want 1", len(items))
	}
	indexPath := filepath.Join(root, OptionalIndexFile)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if index.Format != 1 || len(index.Skills) != 1 {
		t.Fatalf("unexpected index: %+v", index)
	}
	entry := index.Skills[0]
	if entry.Name != "backup/SKILL.md" || entry.Path != "backup/SKILL.md" || entry.Type != "folder" || entry.Source != "local" || entry.SizeBytes != len([]byte(body)) {
		t.Fatalf("unexpected index entry: %+v", entry)
	}
	if strings.Contains(string(data), body) {
		t.Fatal("index contains full skill body")
	}
	if mode := indexMode(t, indexPath); mode != 0o600 {
		t.Fatalf("index mode=%#o, want 0600", mode)
	}

	newDir := filepath.Join(root, "weather")
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "SKILL.md"), []byte("# weather\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshOptionalIndex(root); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "weather/SKILL.md") {
		t.Fatal("index was not updated after adding a skill")
	}
}

func TestRefreshOptionalIndexHandlesMissingAndEmptyRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := RefreshOptionalIndex(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing optional root was created: %v", err)
	}

	empty := t.TempDir()
	items, err := RefreshOptionalIndex(empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("empty root returned %d skills", len(items))
	}
	data, err := os.ReadFile(filepath.Join(empty, OptionalIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if index.Format != 1 || len(index.Skills) != 0 {
		t.Fatalf("unexpected empty index: %+v", index)
	}
}

func indexMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
