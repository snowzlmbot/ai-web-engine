package skills

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOptionalSupportsDirectoryZipAndSkillFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "folder")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# folder skill\nUse folder rules."), 0o600); err != nil {
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
	if _, err := entry.Write([]byte("# zip skill\nUse zip rules.")); err != nil {
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
