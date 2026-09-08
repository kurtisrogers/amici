package canvas

import (
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"strings"
	"testing"
)

// The cases in this file are the security contract for member-authored
// profiles. If one of them starts failing, a profile can attack the people who
// visit it, so treat a failure here as an incident rather than a flaky test.

func TestSanitiseStripsScriptVectors(t *testing.T) {
	t.Parallel()
	// Each input must produce output containing none of the forbidden
	// substrings. They are grouped by the trick they use.
	cases := []struct {
		name string
		in   string
	}{
		{"plain script", `<script>alert(1)</script>`},
		{"script with attrs", `<script type="text/javascript" src="/x.js"></script>`},
		{"uppercase script", `<SCRIPT>alert(1)</SCRIPT>`},
		{"nested broken script", `<scr<script>ipt>alert(1)</script>`},
		{"inline handler", `<div onclick="alert(1)">hi</div>`},
		{"handler odd case", `<div OnMouseOver="alert(1)">hi</div>`},
		{"handler on allowed elem", `<img src="/static/x.png" onerror="alert(1)">`},
		{"javascript href", `<a href="javascript:alert(1)">click</a>`},
		{"javascript href spaced", `<a href=" javascript:alert(1)">click</a>`},
		{"javascript href tabbed", "<a href=\"java\tscript:alert(1)\">click</a>"},
		{"javascript href newline", "<a href=\"java\nscript:alert(1)\">click</a>"},
		{"javascript href mixed case", `<a href="JaVaScRiPt:alert(1)">click</a>`},
		{"vbscript href", `<a href="vbscript:msgbox(1)">click</a>`},
		{"data html href", `<a href="data:text/html;base64,PHNjcmlwdD4=">click</a>`},
		{"iframe", `<iframe src="https://evil.example/"></iframe>`},
		{"object", `<object data="/x.swf"></object>`},
		{"embed", `<embed src="/x.swf">`},
		{"svg onload", `<svg onload="alert(1)"><circle r="10"/></svg>`},
		{"svg script child", `<svg><script>alert(1)</script></svg>`},
		{"math href", `<math><maction actiontype="statusline#javascript:alert(1)">x</maction></math>`},
		{"form phishing", `<form action="https://evil.example/"><input name="password" type="password"><button>Sign in</button></form>`},
		{"style tag", `<style>body{background:url(https://evil.example/pixel.png)}</style>`},
		{"link stylesheet", `<link rel="stylesheet" href="https://evil.example/x.css">`},
		{"base tag", `<base href="https://evil.example/">`},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.example/">`},
		{"video", `<video src="https://evil.example/x.mp4" autoplay></video>`},
		{"audio", `<audio src="https://evil.example/x.mp3" autoplay></audio>`},
		{"noscript", `<noscript><img src="https://evil.example/beacon.gif"></noscript>`},
		{"template", `<template><script>alert(1)</script></template>`},
		{"comment conditional", `<!--[if IE]><script>alert(1)</script><![endif]-->`},
		{"srcdoc", `<iframe srcdoc="<script>alert(1)</script>"></iframe>`},
		{"data attribute", `<div data-payload="alert(1)">hi</div>`},
		{"style expression", `<div style="width: expression(alert(1))">hi</div>`},
		{"style behavior", `<div style="behavior: url(#default#time2)">hi</div>`},
		{"style moz binding", `<div style="-moz-binding: url(https://evil.example/x.xml)">hi</div>`},
		{"style escaped url", `<div style="background: \75 rl(https://evil.example/p.png)">hi</div>`},
		{"style external bg", `<div style="background-image: url(https://evil.example/p.png)">hi</div>`},
		{"external image", `<img src="https://evil.example/beacon.gif">`},
		{"protocol relative image", `<img src="//evil.example/beacon.gif">`},
		{"svg data uri image", `<img src="data:image/svg+xml;base64,PHN2Zz48c2NyaXB0PmFsZXJ0KDEpPC9zY3JpcHQ+PC9zdmc+">`},
		{"applet", `<applet code="Evil.class"></applet>`},
		{"frameset", `<frameset><frame src="https://evil.example/"></frameset>`},
		{"textarea smuggle", `<textarea><script>alert(1)</script></textarea>`},
		{"xmp smuggle", `<xmp><script>alert(1)</script></xmp>`},
		{"dialog overlay", `<dialog open>gotcha</dialog>`},
	}

	forbidden := []string{
		"<script", "javascript:", "vbscript:", "onerror", "onclick",
		"onmouseover", "onload", "<iframe", "<object", "<embed", "<form",
		"<input", "<button", "<style", "<link", "<meta", "<base", "<svg",
		"<math", "<video", "<audio", "<noscript", "<template", "<applet",
		"<frame", "<textarea", "<dialog", "evil.example", "expression(",
		"-moz-binding", "behavior:", "data-payload", "srcdoc",
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := strings.ToLower(Sanitise(tc.in, "").HTML)
			for _, bad := range forbidden {
				if strings.Contains(got, bad) {
					t.Fatalf("sanitised output still contains %q\ninput:  %s\noutput: %s", bad, tc.in, got)
				}
			}
			if strings.Contains(got, "url(") {
				t.Fatalf("sanitised output still contains url()\ninput:  %s\noutput: %s", tc.in, got)
			}
		})
	}
}

func TestSanitiseKeepsNostalgia(t *testing.T) {
	t.Parallel()
	in := `<center><marquee direction="left" scrollamount="6"><font color="#ff66cc" face="Comic Sans MS" size="5">welcome to my page</font></marquee></center>
<table border="1" cellpadding="4" bgcolor="#ffeeff"><tr><td><b>top 8</b></td></tr></table>
<blink>new!</blink>
<div style="background: linear-gradient(45deg, #ff9ecd, #ffd166); border-radius: 12px; padding: 8px">glitter</div>
<img src="/static/stickers/heart.svg" alt="a heart" width="32" height="32">
<details open><summary>my interests</summary><ul><li>my nan</li><li>tomatoes</li></ul></details>`

	got := Sanitise(in, "")
	for _, want := range []string{
		"<center>", "<marquee", `direction="left"`, `scrollamount="6"`,
		"<font", `color="#ff66cc"`, `face="Comic Sans MS"`, "<blink>",
		"linear-gradient", "border-radius", `src="/static/stickers/heart.svg"`,
		"<details", "<summary>", "<li>my nan</li>", `bgcolor="#ffeeff"`,
	} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("expected sanitised output to keep %q\ngot: %s", want, got.HTML)
		}
	}
}

func TestSanitiseForcesLinkSafety(t *testing.T) {
	t.Parallel()
	got := Sanitise(`<a href="https://example.com/friend">a link</a>`, "").HTML
	for _, want := range []string{`href="https://example.com/friend"`, `rel=`, "noopener", "noreferrer", "nofollow", `target="_blank"`} {
		if !strings.Contains(got, want) {
			t.Errorf("expected link safety attribute %q in %s", want, got)
		}
	}
}

func TestSanitiseUnwrapsUnknownElementsKeepingText(t *testing.T) {
	t.Parallel()
	got := Sanitise(`<article><unknown-thing>my words survive</unknown-thing></article>`, "").HTML
	if !strings.Contains(got, "my words survive") {
		t.Errorf("expected text to survive an unsupported wrapper, got %s", got)
	}
	if strings.Contains(got, "unknown-thing") {
		t.Errorf("expected unsupported element to be unwrapped, got %s", got)
	}
}

func TestSanitiseDropsScriptTextEntirely(t *testing.T) {
	t.Parallel()
	got := Sanitise(`<script>var secret = "leak me"</script>`, "").HTML
	if strings.Contains(got, "leak me") {
		t.Errorf("script contents must be dropped, not unwrapped into text: %s", got)
	}
}

func TestSanitiseExplainsRemovals(t *testing.T) {
	t.Parallel()
	res := Sanitise(`<script>alert(1)</script><iframe src="https://x.example"></iframe><img src="https://x.example/p.gif">`, "")
	if len(res.Notices) == 0 {
		t.Fatal("expected notices explaining what was removed")
	}
	joined := strings.ToLower(strings.Join(res.Notices, " | "))
	for _, want := range []string{"script", "iframe", "image"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a notice mentioning %q, got: %v", want, res.Notices)
		}
	}
}

func TestSanitiseBoundsNesting(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("<div>", 500) + "bottom" + strings.Repeat("</div>", 500)
	got := Sanitise(deep, "").HTML
	if strings.Count(got, "<div") > MaxDepth+1 {
		t.Errorf("expected nesting to be capped at %d, got %d", MaxDepth, strings.Count(got, "<div"))
	}
}

func TestSanitiseBoundsElementCount(t *testing.T) {
	t.Parallel()
	many := strings.Repeat(`<span>x</span>`, MaxElements+500)
	got := Sanitise(many, "").HTML
	if n := strings.Count(got, "<span"); n > MaxElements {
		t.Errorf("expected at most %d elements, got %d", MaxElements, n)
	}
}

func TestSanitiseAcceptsSmallRasterDataURI(t *testing.T) {
	t.Parallel()
	// A 1x1 transparent GIF, the traditional building block of a spacer.
	const gif = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="
	got := Sanitise(`<img src="`+gif+`" alt="spacer">`, "").HTML
	if !strings.Contains(got, "data:image/gif;base64,") {
		t.Errorf("expected a small raster data URI to survive, got %s", got)
	}
}

func TestStylesheetAllowsDecorationAndScopesIt(t *testing.T) {
	t.Parallel()
	css, notices := Stylesheet(`
		body { background: linear-gradient(#fdd, #dff); color: #402 }
		.top8 img:hover { transform: rotate(3deg) scale(1.1) }
		@media (max-width: 600px) { .top8 { display: block } }
		@keyframes wiggle { from { rotate: -2deg } to { rotate: 2deg } }
	`)
	for _, want := range []string{
		rootSelector + " {", "linear-gradient", rootSelector + " .top8 img:hover",
		"@media (max-width: 600px)", "@keyframes wiggle", "transform: rotate(3deg) scale(1.1)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("expected sanitised CSS to contain %q\ngot: %s\nnotices: %v", want, css, notices)
		}
	}
}

func TestStylesheetRejectsDangerousConstructs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     string
		absent string
	}{
		{"import", `@import url("https://evil.example/x.css"); .a { color: red }`, "@import"},
		{"font face", `@font-face { font-family: x; src: url(https://evil.example/f.woff) }`, "@font-face"},
		{"background url", `.a { background-image: url(https://evil.example/p.png) }`, "url("},
		{"escaped url", `.a { background-image: \75 rl(https://evil.example/p.png) }`, "rl("},
		{"expression", `.a { width: expression(alert(1)) }`, "expression"},
		{"moz binding", `.a { -moz-binding: url(https://evil.example/x.xml) }`, "binding"},
		{"behavior", `.a { behavior: url(#default#time2) }`, "behavior"},
		{"position fixed", `.a { position: fixed; top: 0 }`, "fixed"},
		{"position sticky", `.a { position: sticky }`, "sticky"},
		{"image set", `.a { background-image: image-set("x.png" 1x) }`, "image-set"},
		{"element function", `.a { background: element(#x) }`, "element("},
		{"vendor prefixed banned prop", `.a { -moz-binding: url(x) }`, "binding"},
		{"attr in content", `.a::after { content: attr(href) }`, "attr("},
		{"unknown at rule", `@document url-prefix() { .a { color: red } }`, "@document"},
		{"selector escape", `.a\3a hover { color: red }`, "\\"},
		{"html import in media", `@media (min-width: 1px) { @import "x.css"; }`, "@import"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			css, _ := Stylesheet(tc.in)
			if strings.Contains(strings.ToLower(css), strings.ToLower(tc.absent)) {
				t.Fatalf("sanitised CSS still contains %q\ninput:  %s\noutput: %s", tc.absent, tc.in, css)
			}
		})
	}
}

func TestStylesheetRejectsEscapesWholesale(t *testing.T) {
	t.Parallel()
	css, notices := Stylesheet(`.a { color: \72 ed }`)
	if css != "" {
		t.Errorf("a stylesheet containing a backslash must be rejected entirely, got %q", css)
	}
	if len(notices) == 0 {
		t.Error("expected a notice explaining that escapes are unsupported")
	}
}

func TestStylesheetCannotEscapeItsScope(t *testing.T) {
	t.Parallel()
	css, _ := Stylesheet(`html, body, :root, * { color: red } .x { color: blue }`)
	for _, line := range strings.Split(css, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "@") || strings.HasPrefix(line, "}") {
			continue
		}
		if !strings.HasPrefix(line, rootSelector) {
			t.Errorf("every rule must be scoped to %s, found: %q", rootSelector, line)
		}
	}
}

func TestStylesheetCapsRuleCount(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	for i := 0; i < maxRules+50; i++ {
		sb.WriteString(".r { color: red }\n")
	}
	css, notices := Stylesheet(sb.String())
	if n := strings.Count(css, "{"); n > maxRules {
		t.Errorf("expected at most %d rules, got %d", maxRules, n)
	}
	if len(notices) == 0 {
		t.Error("expected a notice about the rule limit")
	}
}

func TestDeclarationsRejectsUnknownProperties(t *testing.T) {
	t.Parallel()
	n := newNotices()
	got, count := Declarations(`color: red; totally-made-up: 4; padding: 2px`, n)
	if strings.Contains(got, "totally-made-up") {
		t.Errorf("unknown property survived: %s", got)
	}
	if count != 2 {
		t.Errorf("expected 2 surviving declarations, got %d (%s)", count, got)
	}
	if len(n.list()) == 0 {
		t.Error("expected a notice naming the rejected property")
	}
}

func TestDeclarationsNormalisesVendorPrefixesBeforeChecking(t *testing.T) {
	t.Parallel()
	n := newNotices()
	got, _ := Declarations(`-webkit-transform: rotate(2deg); -moz-binding: none`, n)
	if !strings.Contains(got, "transform") {
		t.Errorf("expected a prefixed allowlisted property to be accepted, got %s", got)
	}
	if strings.Contains(got, "binding") {
		t.Errorf("a prefix must not smuggle a banned property through: %s", got)
	}
}

func TestSanitiseIsIdempotent(t *testing.T) {
	t.Parallel()
	// Re-sanitising stored output must not change it. If it did, our stored
	// rendering would drift away from what a fresh save would produce, and the
	// re-render migration described in the docs would rewrite every profile.
	in := `<center><marquee><font color="red" size="4">hi</font></marquee></center>
	<a href="https://example.com/x">link</a>
	<div style="color: #123456; background: linear-gradient(45deg, #fff, #000)">x</div>
	<img src="/static/stickers/heart.svg" alt="h" width="20">`
	once := Sanitise(in, ".a { color: red }")
	twice := Sanitise(once.HTML, once.CSS)
	if once.HTML != twice.HTML {
		t.Errorf("HTML sanitising is not idempotent:\nfirst:  %s\nsecond: %s", once.HTML, twice.HTML)
	}
}

func FuzzSanitiseNeverEmitsScript(f *testing.F) {
	seeds := []string{
		`<script>alert(1)</script>`,
		`<div style="x:\75 rl(y)">`,
		`<a href="javascript:alert(1)">x</a>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`<<script>script>alert(1)<</script>/script>`,
		`<style>@import url(x)</style>`,
		`<marquee behavior=alternate>`,
	}
	for _, s := range seeds {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, h, c string) {
		res := Sanitise(h, c)

		// Assert on the parsed tree rather than on substrings. Text content is
		// allowed to say "onload=" or "<script"; what must never happen is for
		// those to come back as an attribute or an element.
		doc, err := html.ParseFragment(strings.NewReader(res.HTML), &html.Node{
			Type: html.ElementNode, Data: "div", DataAtom: atom.Div,
		})
		if err != nil {
			t.Fatalf("sanitised output does not reparse: %v\ninput: %q\noutput: %q", err, h, res.HTML)
		}
		for _, root := range doc {
			walk(root, func(n *html.Node) {
				if n.Type != html.ElementNode {
					return
				}
				name := strings.ToLower(n.Data)
				if !allowedElements[name] {
					t.Fatalf("element <%s> survived sanitising\ninput: %q\noutput: %q", name, h, res.HTML)
				}
				for _, a := range n.Attr {
					key := strings.ToLower(a.Key)
					if strings.HasPrefix(key, "on") {
						t.Fatalf("event handler attribute %q survived\ninput: %q\noutput: %q", key, h, res.HTML)
					}
					if !globalAttrs[key] && !elementAttrs[name][key] &&
						key != "target" && key != "rel" && key != "loading" {
						t.Fatalf("attribute %q on <%s> survived\ninput: %q\noutput: %q", key, name, h, res.HTML)
					}
					val := strings.ToLower(a.Val)
					if key == "href" || key == "src" || key == "cite" {
						if !safeURLValue(val) {
							t.Fatalf("unsafe %s=%q survived\ninput: %q", key, a.Val, h)
						}
					}
					if key == "style" {
						for _, bad := range []string{"url(", "expression", "\\", "behavior", "binding"} {
							if strings.Contains(val, bad) {
								t.Fatalf("style attribute contains %q: %q\ninput: %q", bad, a.Val, h)
							}
						}
					}
				}
			})
		}

		lowCSS := strings.ToLower(res.CSS)
		for _, bad := range []string{"url(", "expression", "@import", "@font-face", "\\", "</", "<script", "javascript:"} {
			if strings.Contains(lowCSS, bad) {
				t.Fatalf("sanitised CSS contains %q for input %q: %s", bad, c, res.CSS)
			}
		}
	})
}

func safeURLValue(v string) bool {
	switch {
	case v == "":
		return true
	case strings.HasPrefix(v, "#"):
		return true
	case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"):
		return true
	case strings.HasPrefix(v, "mailto:"):
		return true
	case strings.HasPrefix(v, "data:image/"):
		return !strings.Contains(v, "svg")
	case strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//"):
		return true
	default:
		return false
	}
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}
