package service

import (
	"context"
	"strings"

	"go.kenn.io/agentsview/internal/db"
)

const inputOutlinePreviewMaxRunes = 160

func (b *directBackend) InputOutline(
	ctx context.Context, id string, includeForkContext bool,
) (*InputOutline, error) {
	if includeForkContext {
		msgs, err := b.buildForkContextMessages(ctx, id)
		if err != nil {
			return nil, err
		}
		return inputOutlineFromMessages(msgs), nil
	}
	rows, err := b.db.GetInputOutline(ctx, id)
	if err != nil {
		return nil, err
	}
	items := make([]InputOutlineItem, 0, len(rows))
	for _, row := range rows {
		if item, ok := inputOutlineItem(
			row.Ordinal, row.Timestamp, row.Content,
		); ok {
			items = append(items, item)
		}
	}
	return &InputOutline{Items: items, Count: len(items)}, nil
}

func inputOutlineFromMessages(msgs []db.Message) *InputOutline {
	items := make([]InputOutlineItem, 0)
	for _, msg := range msgs {
		if msg.Role != "user" || msg.IsSystem ||
			db.IsSystemPrefixed(msg.Content, msg.Role) {
			continue
		}
		if item, ok := inputOutlineItem(
			msg.Ordinal, msg.Timestamp, msg.Content,
		); ok {
			items = append(items, item)
		}
	}
	return &InputOutline{Items: items, Count: len(items)}
}

func inputOutlineItem(
	ordinal int, timestamp string, content string,
) (InputOutlineItem, bool) {
	preview, isShell := normalizeInputOutlinePreview(content)
	if preview == "" {
		return InputOutlineItem{}, false
	}
	return InputOutlineItem{
		Ordinal:   ordinal,
		Timestamp: timestamp,
		Preview:   preview,
		IsShell:   isShell,
	}, true
}

func normalizeInputOutlinePreview(content string) (string, bool) {
	isShell := hasBashWrapper(content)
	text := content
	if isShell {
		if body, ok := firstWrappedContent(text, "bash-input"); ok {
			text = body
		} else {
			text = stripBashWrapperTags(text)
		}
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		return truncateInputOutlinePreview(line), isShell
	}
	return "", isShell
}

func hasBashWrapper(content string) bool {
	return strings.Contains(content, "<bash-input>") ||
		strings.Contains(content, "<bash-stdout>") ||
		strings.Contains(content, "<bash-stderr>")
}

func firstWrappedContent(content, tag string) (string, bool) {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(content, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(content[start:], close)
	if end < 0 {
		return content[start:], true
	}
	return content[start : start+end], true
}

func stripBashWrapperTags(content string) string {
	replacements := []string{
		"<bash-input>", "",
		"</bash-input>", "",
		"<bash-stdout>", "",
		"</bash-stdout>", "",
		"<bash-stderr>", "",
		"</bash-stderr>", "",
	}
	out := content
	for i := 0; i < len(replacements); i += 2 {
		out = strings.ReplaceAll(out, replacements[i], replacements[i+1])
	}
	return out
}

func truncateInputOutlinePreview(preview string) string {
	runes := []rune(preview)
	if len(runes) <= inputOutlinePreviewMaxRunes {
		return preview
	}
	if inputOutlinePreviewMaxRunes <= 3 {
		return string(runes[:inputOutlinePreviewMaxRunes])
	}
	return string(runes[:inputOutlinePreviewMaxRunes-3]) + "..."
}
