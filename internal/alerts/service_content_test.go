package alerts

import (
	"strings"
	"testing"
)

func TestServiceProviderContentTreatsAliasesAsLiteralData(t *testing.T) {
	n := Notification{ServiceID: "service", Title: "MyProbe 告警", Message: "服务 <b>alias</b> & [spoof](https://example.test) *bold*", IncidentPath: "https://probe.example.test/admin?incident=original"}
	got := telegramContent(n, "HTML")
	if strings.Contains(got, "<b>alias</b>") || !strings.Contains(got, "&lt;b&gt;alias&lt;/b&gt; &amp;") || !strings.HasSuffix(got, `<a href="`+n.IncidentPath+`">查看事件</a>`) {
		t.Fatal("service alias became markup or generated link was escaped")
	}
	for _, mode := range []string{"MarkdownV2", "discord"} {
		got := telegramContent(n, mode)
		if mode == "discord" {
			got = discordContent(n)
		}
		if strings.Contains(got, "[spoof](") || strings.Contains(got, "*bold*") || !strings.Contains(got, `\[spoof\]\(`) || !strings.Contains(got, n.IncidentPath) {
			t.Fatal("service alias became markup or link was lost", mode)
		}
	}
	if got := telegramContent(n, ""); got != n.Title+"\n"+n.Message+"\n查看事件："+n.IncidentPath {
		t.Fatal("plain text changed")
	}
}
