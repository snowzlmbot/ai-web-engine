package contextx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

type Reader interface {
	StreamRead(root, rel string, dst io.Writer) (int64, error)
}

type Options struct {
	MaxMessages       int
	MaxMessageRunes   int
	MaxReferenceRunes int
	ReferenceRoot     string
	ReferenceNames    []string
	TotalContextRunes int
}

type Result struct {
	Messages   []session.Message
	References []Reference
	Compacted  bool
	Notice     string
	Sources    []string
}

type Reference struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// Compact keeps the system constraints/current request and recent conversation
// intact. Older messages are replaced by explicit, source-labelled summaries;
// no message disappears silently.
func Compact(messages []session.Message, options Options) Result {
	if options.MaxMessages <= 0 {
		options.MaxMessages = 24
	}
	if options.MaxMessageRunes <= 0 {
		options.MaxMessageRunes = 12000
	}
	messages = dedupe(messages)
	if len(messages) <= options.MaxMessages && totalRunes(messages) <= options.MaxMessageRunes {
		return Result{Messages: messages}
	}

	// System messages carry the official skill and policy constraints. They are
	// protected even when the configured conversational budget is smaller than
	// the system prompt; silently dropping one would change the task contract.
	selected := make(map[int]bool, len(messages))
	usedRunes := 0
	for index, message := range messages {
		if message.Role != "system" {
			continue
		}
		selected[index] = true
		usedRunes += len([]rune(message.Content))
	}

	// Always retain the current user request, even if it is larger than the
	// normal budget. The source file/session remains readable; only older
	// conversational context is compressed.
	currentUser := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			currentUser = index
			break
		}
	}
	if currentUser >= 0 && !selected[currentUser] {
		selected[currentUser] = true
		usedRunes += len([]rune(messages[currentUser].Content))
	}

	for index := len(messages) - 1; index >= 0; index-- {
		if selected[index] {
			continue
		}
		if len(selected) >= options.MaxMessages {
			break
		}
		messageRunes := len([]rune(messages[index].Content))
		if usedRunes+messageRunes > options.MaxMessageRunes {
			continue
		}
		selected[index] = true
		usedRunes += messageRunes
	}

	kept := make([]session.Message, 0, len(selected)+1)
	var omitted []string
	for index, message := range messages {
		if selected[index] {
			kept = append(kept, message)
			continue
		}
		omitted = append(omitted, message.Role+":"+fingerprint(message.Content))
	}
	if len(omitted) == 0 {
		return Result{Messages: kept}
	}
	summary := fmt.Sprintf("[context-summary]\n已压缩 %d 条较早消息；摘要指纹：%s。官方 system 约束与当前用户请求已保留；必要原文未被伪造，如需可按会话消息 ID 重新读取。", len(omitted), strings.Join(omitted, ","))
	insertAt := 0
	for insertAt < len(kept) && kept[insertAt].Role == "system" {
		insertAt++
	}
	kept = append(kept, session.Message{})
	copy(kept[insertAt+1:], kept[insertAt:])
	kept[insertAt] = session.Message{ID: "context-summary", Role: "system", Content: summary}
	return Result{Messages: dedupe(kept), Compacted: true, Notice: "旧消息已显式摘要，官方 system 约束和当前用户请求保留"}
}

func ReadReferences(reader Reader, root string, names []string, maxRunes int) ([]Reference, error) {
	if maxRunes <= 0 {
		maxRunes = 20000
	}
	result := make([]Reference, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		collector := &runeCollector{limit: maxRunes}
		if _, err := reader.StreamRead(root, name, collector); err != nil {
			continue
		}
		result = append(result, Reference{Name: name, Content: collector.String(), Truncated: collector.truncated})
	}
	return result, nil
}

type runeCollector struct {
	limit     int
	count     int
	buffer    strings.Builder
	truncated bool
}

func (c *runeCollector) Write(data []byte) (int, error) {
	original := len(data)
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			// Keep malformed bytes representable instead of rejecting the source
			// file. The context receives a safe replacement rune.
			r = utf8.RuneError
		}
		if c.count < c.limit {
			c.buffer.WriteRune(r)
			c.count++
		} else {
			c.truncated = true
		}
		data = data[size:]
	}
	return original, nil
}

func (c *runeCollector) String() string { return c.buffer.String() }

func dedupe(messages []session.Message) []session.Message {
	seen := map[string]bool{}
	result := make([]session.Message, 0, len(messages))
	for _, message := range messages {
		key := message.Role + "\x00" + message.Content
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, message)
	}
	return result
}

func totalRunes(messages []session.Message) int {
	total := 0
	for _, message := range messages {
		total += len([]rune(message.Content))
	}
	return total
}

func fingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:6])
}
