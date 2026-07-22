package sanitize

import (
	"strings"
	"testing"
)

func TestHTMLAllowsFormattingAndSafeLinks(t *testing.T) {
	got, err := HTML(`<p><strong>Status</strong> <a href="https://example.com?a=1&amp;b=2" title="Docs">open</a></p>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `<strong>Status</strong>`) || !strings.Contains(got, `href="https://example.com?a=1&amp;b=2"`) || !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Fatalf("sanitized HTML = %s", got)
	}
}

func TestHTMLRemovesExecutableAndUnsafeContent(t *testing.T) {
	got, err := HTML(`<img src=x onerror=alert(1)><script>alert(1)</script><a href="javascript:alert(1)" onclick="x()">bad</a><svg><a href="x">svg</a></svg><b style="color:red">safe</b>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"script", "javascript", "onclick", "onerror", "style=", "svg", "alert(1)"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("sanitized HTML contains %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "<b>safe</b>") || !strings.Contains(got, "<a rel=\"noopener noreferrer\">bad</a>") {
		t.Fatalf("sanitized HTML = %s", got)
	}
}
