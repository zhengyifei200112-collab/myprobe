package alerts

import (
	"html"
	"net/url"
	"strings"
	"unicode"
)

func textUnits(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}
func escapeMarkdownText(s, special string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Aggregate titles/excerpts are data, even when a source template used markup.
// Keep a generated absolute content URL separate so escaping cannot break it.
func aggregateContentParts(n Notification) (string, string) {
	text := n.Title + "\n" + n.Message
	suffix := "\n查看本次内容：" + n.GroupPath
	if n.GroupPath == "" || !strings.HasSuffix(text, suffix) {
		return text, ""
	}
	u, err := url.Parse(n.GroupPath)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || strings.ContainsAny(n.GroupPath, "<>\"\r\n") || strings.IndexFunc(n.GroupPath, unicode.IsSpace) >= 0 {
		return text, ""
	}
	return strings.TrimSuffix(text, suffix), n.GroupPath
}
func telegramContent(n Notification, mode string) string {
	text := n.Title + "\n" + n.Message
	if n.AggregateID == "" {
		return text
	} // Preserve the existing single-template contract.
	text, link := aggregateContentParts(n)
	switch mode {
	case "HTML":
		text = html.EscapeString(text)
		if link != "" {
			text += "\n<a href=\"" + html.EscapeString(link) + "\">查看本次内容</a>"
		}
	case "MarkdownV2":
		text = escapeMarkdownText(text, "\\_*[]()~`>#+-=|{}.!")
		if link != "" {
			target := strings.NewReplacer("\\", "\\\\", ")", "\\)").Replace(link)
			text += "\n[查看本次内容](" + target + ")"
		}
	default:
		if link != "" {
			text += "\n查看本次内容：" + link
		}
	}
	return text
}
func discordContent(n Notification) string {
	if n.AggregateID == "" {
		return "**" + n.Title + "**\n" + n.Message
	}
	text, link := aggregateContentParts(n)
	suffix := ""
	if link != "" {
		suffix = "\n[查看本次内容](<" + link + ">)"
	}
	return escapeMarkdownLimited(text, "\\_*[]()~`>#+-=|{}.!", 2000-textUnits(suffix)) + suffix
}

func escapeMarkdownLimited(raw, special string, limit int) string {
	if textUnits(escapeMarkdownText(raw, special)) <= limit {
		return escapeMarkdownText(raw, special)
	}
	if limit < 1 {
		return ""
	}
	var b strings.Builder
	remaining := limit - 1
	for _, r := range raw {
		width := 1
		if r > 0xffff {
			width++
		}
		escaped := strings.ContainsRune(special, r)
		if escaped {
			width++
		}
		if width > remaining {
			break
		}
		remaining -= width
		if escaped {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}
