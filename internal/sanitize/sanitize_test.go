package sanitize

import (
	"strings"
	"testing"
)

func TestHTML_StripsScripts(t *testing.T) {
	got := HTML(`<p>hello</p><script>alert(1)</script>`)
	if got != "<p>hello</p>" {
		t.Errorf("HTML() = %q, want script removed", got)
	}
}

func TestHTML_StripsInlineEventHandlers(t *testing.T) {
	got := HTML(`<p onclick="evil()">hi</p>`)
	if got != "<p>hi</p>" {
		t.Errorf("HTML() = %q, want onclick removed", got)
	}
}

func TestHTML_StripsForms(t *testing.T) {
	got := HTML(`<form action="https://evil.example/steal"><input name="x"></form>`)
	if got != "" {
		t.Errorf("HTML() = %q, want form removed entirely", got)
	}
}

func TestHTML_StripsUnsafeElements(t *testing.T) {
	for _, tc := range []string{
		`<iframe src="https://evil.example"></iframe>`,
		`<object data="evil.swf"></object>`,
		`<embed src="evil.swf">`,
		`<style>body{background:url(javascript:alert(1))}</style>`,
		`<link rel="stylesheet" href="https://evil.example/x.css">`,
	} {
		if got := HTML(tc); got != "" {
			t.Errorf("HTML(%q) = %q, want stripped entirely", tc, got)
		}
	}
}

func TestHTML_KeepsSafeFormatting(t *testing.T) {
	in := `<p>Hello <b>World</b>, see <a href="https://example.com">this</a>.</p>`
	got := HTML(in)
	if got == "" {
		t.Fatalf("HTML() stripped everything, want safe tags preserved")
	}
	for _, want := range []string{"<p>", "<b>World</b>", `href="https://example.com"`} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML() = %q, want to contain %q", got, want)
		}
	}
}

func TestHTML_RejectsJavascriptURLs(t *testing.T) {
	got := HTML(`<a href="javascript:alert(1)">click</a>`)
	if strings.Contains(got, "javascript:") {
		t.Errorf("HTML() = %q, want javascript: URL stripped", got)
	}
}

func TestRewriteImageSrcs_RewritesSrcAndDropsSrcset(t *testing.T) {
	in := HTML(`<img src="https://example.com/cat.png" srcset="https://example.com/cat2x.png 2x">`)
	got, err := RewriteImageSrcs(in, func(src string) string {
		return "/proxy/image?url=" + src
	})
	if err != nil {
		t.Fatalf("RewriteImageSrcs() error = %v", err)
	}
	if !strings.Contains(got, `src="/proxy/image?url=https://example.com/cat.png"`) {
		t.Errorf("RewriteImageSrcs() = %q, want rewritten src", got)
	}
	if strings.Contains(got, "srcset") {
		t.Errorf("RewriteImageSrcs() = %q, want srcset dropped", got)
	}
}
