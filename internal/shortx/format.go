package shortx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Separator is the official ShortX text-file boundary between the instruction
// object and its type marker.
const Separator = "###------###"

type Document struct {
	Kind      string
	ID        string
	Title     string
	Canonical string
}

// Parse validates a complete ShortX import file and returns a canonical UTF-8
// representation. It deliberately does not execute or interpret any action.
func Parse(input string) (Document, error) {
	if !utf8.ValidString(input) {
		return Document{}, errors.New("ShortX 指令必须是有效的 UTF-8 文本")
	}
	text := strings.ReplaceAll(input, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return Document{}, errors.New("ShortX 指令为空")
	}
	separatorLines := 0
	for _, line := range strings.Split(text, "\n") {
		if line == Separator {
			separatorLines++
		}
	}
	if separatorLines != 1 {
		return Document{}, fmt.Errorf("ShortX 分隔符必须独占一行且只能出现一次")
	}
	parts := strings.SplitN(text, "\n"+Separator+"\n", 2)
	if len(parts) != 2 {
		return Document{}, fmt.Errorf("ShortX 分隔符格式无效")
	}
	mainText := strings.TrimSpace(parts[0])
	trailerText := strings.TrimSpace(parts[1])

	var main map[string]any
	if err := json.Unmarshal([]byte(mainText), &main); err != nil {
		return Document{}, fmt.Errorf("主体 JSON 无效: %w", err)
	}
	if main == nil {
		return Document{}, errors.New("主体 JSON 必须是对象")
	}
	var trailer map[string]any
	if err := json.Unmarshal([]byte(trailerText), &trailer); err != nil {
		return Document{}, fmt.Errorf("尾部 JSON 无效: %w", err)
	}
	if trailer == nil {
		return Document{}, errors.New("尾部 JSON 必须是对象")
	}
	kind, ok := trailer["type"].(string)
	if !ok || (kind != "rule" && kind != "da") {
		return Document{}, errors.New(`尾部 type 必须是 "rule" 或 "da"`)
	}

	if err := validateString(main, "id"); err != nil {
		return Document{}, err
	}
	if err := validateString(main, "title"); err != nil {
		return Document{}, err
	}
	if err := validateString(main, "description"); err != nil {
		return Document{}, err
	}
	if err := validateString(main, "versionCode"); err != nil {
		return Document{}, err
	}
	if err := validateArray(main, "actions"); err != nil {
		return Document{}, err
	}
	if err := validateObject(main, "hook"); err != nil {
		return Document{}, err
	}
	if err := validateObject(main, "quit"); err != nil {
		return Document{}, err
	}
	if err := validateArray(main, "parameters"); err != nil {
		return Document{}, err
	}
	if kind == "rule" {
		if err := validateArray(main, "facts"); err != nil {
			return Document{}, err
		}
		if err := validateArray(main, "conditions"); err != nil {
			return Document{}, err
		}
	}

	mainCanonical, err := marshalCanonical(main)
	if err != nil {
		return Document{}, fmt.Errorf("规范化主体 JSON 失败: %w", err)
	}
	trailerCanonical := fmt.Sprintf(`{"type":"%s"}`, kind)
	return Document{
		Kind:      kind,
		ID:        main["id"].(string),
		Title:     main["title"].(string),
		Canonical: mainCanonical + "\n" + Separator + "\n" + trailerCanonical + "\n",
	}, nil
}

func validateString(object map[string]any, key string) error {
	value, ok := object[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("字段 %q 必须是非空字符串", key)
	}
	return nil
}

func validateArray(object map[string]any, key string) error {
	if _, ok := object[key].([]any); !ok {
		return fmt.Errorf("字段 %q 必须是数组", key)
	}
	return nil
}

func validateObject(object map[string]any, key string) error {
	if _, ok := object[key].(map[string]any); !ok {
		return fmt.Errorf("字段 %q 必须是对象", key)
	}
	return nil
}

func marshalCanonical(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}
