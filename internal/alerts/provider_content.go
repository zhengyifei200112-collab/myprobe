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
		if n.ServiceID != "" {
			if mode == "HTML" {
				text = html.EscapeString(text)
			} else if mode == "MarkdownV2" {
				text = escapeMarkdownText(text, "\\_*[]()~`>#+-=|{}.!")
			}
		}
		return text + incidentContentSuffix(n.IncidentPath, mode)
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
		if n.ServiceID != "" {
			return escapeMarkdownText(n.Title+"\n"+n.Message, "\\_*[]()~`>#+-=|{}.!") + incidentContentSuffix(n.IncidentPath, "discord")
		}
		return "**" + n.Title + "**\n" + n.Message + incidentContentSuffix(n.IncidentPath, "discord")
	}
	text, link := aggregateContentParts(n)
	suffix := ""
	if link != "" {
		suffix = "\n[查看本次内容](<" + link + ">)"
	}
	return escapeMarkdownLimited(text, "\\_*[]()~`>#+-=|{}.!", 2000-textUnits(suffix)) + suffix
}

// Existing single-message template markup is retained; only the generated URL
// is formatted. Relative paths remain copyable when no public origin is set.
func incidentContentSuffix(path, mode string) string {
	if path == "" {
		return ""
	}
	u, err := url.Parse(path)
	if err != nil || u.User != nil || strings.ContainsAny(path, "<>\"\r\n") || strings.IndexFunc(path, unicode.IsSpace) >= 0 {
		return ""
	}
	absolute := (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
	if !absolute && !(u.Scheme == "" && u.Host == "" && strings.HasPrefix(path, "/admin?incident=")) {
		return ""
	}
	switch mode {
	case "HTML":
		if absolute {
			return "\n<a href=\"" + html.EscapeString(path) + "\">查看事件</a>"
		}
		return "\n查看事件：" + html.EscapeString(path)
	case "MarkdownV2":
		if absolute {
			return "\n[查看事件](" + strings.NewReplacer("\\", "\\\\", ")", "\\)").Replace(path) + ")"
		}
		return "\n查看事件：" + escapeMarkdownText(path, "\\_*[]()~`>#+-=|{}.!")
	case "discord":
		if absolute {
			return "\n[查看事件](<" + path + ">)"
		}
		return "\n查看事件：" + escapeMarkdownText(path, "\\_*[]()~`>#+-=|{}.!")
	default:
		return "\n查看事件：" + path
	}
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
