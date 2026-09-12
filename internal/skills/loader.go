package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Skill struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Text string `json:"-"`
}

func Load(root string) ([]Skill, string, error) {
	var result []Skill
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() != "SKILL.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, Skill{Name: filepath.ToSlash(rel), Path: path, Text: string(data)})
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("scan skills: %w", err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	var b strings.Builder
	b.WriteString("你必须严格遵守以下 skills 约束：\n\n")
	for _, skill := range result {
		b.WriteString("--- SKILL: ")
		b.WriteString(skill.Name)
		b.WriteString(" ---\n")
		b.WriteString(skill.Text)
		b.WriteString("\n---\n\n")
	}
	return result, b.String(), nil
}
