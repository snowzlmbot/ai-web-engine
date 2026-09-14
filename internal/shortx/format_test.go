package shortx

import (
	"strings"
	"testing"
)

func validDirectAction() string {
	return `{
  "actions": [],
  "id": "DA-TEST-GENERATE-001",
  "title": "测试：一键生成指令",
  "description": "仅用于测试 ShortX 指令导入与一键执行能力；不执行任何设备操作。",
  "versionCode": "1",
  "hook": {},
  "quit": {},
  "parameters": []
}
###------###
{"type":"da"}
`
}

func TestParseDirectActionCanonicalizesOfficialFile(t *testing.T) {
	doc, err := Parse(validDirectAction())
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "da" || doc.ID != "DA-TEST-GENERATE-001" || doc.Title != "测试：一键生成指令" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if strings.Contains(doc.Canonical, "```") || !strings.Contains(doc.Canonical, Separator) || !strings.HasSuffix(doc.Canonical, "{\"type\":\"da\"}\n") {
		t.Fatalf("canonical format is not official: %q", doc.Canonical)
	}
	if _, err := Parse(doc.Canonical); err != nil {
		t.Fatalf("canonical document does not round trip: %v", err)
	}
}

func TestParseRuleRequiresRuleCollections(t *testing.T) {
	input := `{
  "facts": [],
  "conditions": [],
  "actions": [],
  "id": "RULE-TEST-001",
  "title": "测试自动指令",
  "description": "只测试规则文件格式。",
  "isEnabled": true,
  "condOp": "ALL",
  "hook": {},
  "quit": {},
  "parameters": [],
  "versionCode": "1"
}
###------###
{"type":"rule"}`
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "rule" {
		t.Fatalf("kind = %q", doc.Kind)
	}
}

func TestParseRejectsMalformedOrWrongOfficialBoundary(t *testing.T) {
	cases := []string{
		strings.Replace(validDirectAction(), "###------###", "###-----###", 1),
		strings.Replace(validDirectAction(), "###------###", "###------###\n###------###", 1),
		strings.Replace(validDirectAction(), `{"type":"da"}`, `{"type":"rule"}`, 1),
		strings.Replace(validDirectAction(), `"actions": []`, `"actions": {}`, 1),
	}
	for i, input := range cases {
		if _, err := Parse(input); err == nil {
			t.Fatalf("case %d was accepted", i)
		}
	}
}

func TestParsePreservesRequiredSymbolsInsideInstructionStrings(t *testing.T) {
	input := strings.Replace(validDirectAction(), `"description": "仅用于测试 ShortX 指令导入与一键执行能力；不执行任何设备操作。"`, `"description": "保留 $HOME ${value} \\n ###------### 引号 \" 和尖括号 < >。"`, 1)
	// The extra separator in a JSON string is not a valid file boundary; this
	// test instead verifies that a legal command string keeps shell symbols.
	input = strings.Replace(input, ` ###------### `, ` 分隔符 `, 1)
	doc, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"$HOME", "${value}", `\n`, `\"`, "<", ">"} {
		if !strings.Contains(doc.Canonical, symbol) {
			t.Fatalf("canonical output lost symbol %q: %q", symbol, doc.Canonical)
		}
	}
}
