// Package canvas turns member-authored HTML and CSS into markup that is safe
// to serve to their friends.
//
// # Why this exists
//
// Half the joy of the early social web was that your page looked like you made
// it, because you did. Amici brings that back. The cost is that we are now in
// the business of accepting markup from one member and rendering it in another
// member's browser, which is the definition of a stored cross-site scripting
// sink. So the feature is built as five independent layers, each of which
// would have to fail before anything bad reaches a viewer.
//
//  1. This sanitiser. A strict allowlist of elements, attributes, URL schemes
//     and CSS properties, applied when the member saves. Anything not
//     recognised is dropped and the member is told why.
//
//  2. A second, independent sanitiser. The output of layer 1 is run through
//     bluemonday with an equally strict policy. Two allowlist sanitisers from
//     different authors, in series, means a parsing quirk in one has to be
//     matched by the same quirk in the other to get through.
//
//  3. Rendering only ever happens in a dedicated document. Canvas markup is
//     never interpolated into an Amici page. It is served from its own
//     endpoint and embedded in an iframe, so it shares no DOM with the
//     session-bearing application.
//
//  4. That iframe is sandboxed without allow-scripts and without
//     allow-same-origin. No scripts can run, and the frame's origin is opaque,
//     so even a hypothetical script could not read Amici's cookies, its DOM,
//     or make a credentialed request back to it.
//
//  5. The canvas document carries a Content-Security-Policy of
//     default-src 'none' with img-src limited to our own origin and data URIs.
//     No script, no fetch, no font, no frame, no third-party anything.
//
// Layers 3 to 5 live in internal/web; this package is layers 1 and 2.
//
// # Why no external resources at all
//
// The easy version of this feature lets people hotlink a glittery background
// from wherever they found it. We do not, and it is not laziness. Every
// external URL in a profile is a request from the viewer's browser to a
// stranger's server, carrying the viewer's IP address, the time they looked,
// and often enough to fingerprint them. Amici is built so that people cannot
// be found; leaking every visitor's address to an image host would undo that
// for a tiled background. Local decoration is provided by a bundled sticker
// set, and members can still write as much gradient-soaked CSS as they like.
package canvas

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Version identifies the sanitiser that produced a stored rendering. Bumping
// it lets a migration find canvases rendered by older, looser rules and put
// them through the current sanitiser again.
const Version = 1

// Structural limits. These are about keeping one member's page from being a
// denial of service against everyone who visits it.
const (
	MaxDepth        = 32
	MaxElements     = 2000
	MaxDataURILen   = 256 * 1024
	MaxTextRunLen   = 20000
	MaxAttrValueLen = 2000
)

// Result is a sanitised canvas.
type Result struct {
	HTML    string
	CSS     string
	Notices []string
}

// allowedElements is the element allowlist. It is generous about decoration
// and silent about anything that can execute, navigate, fetch, or collect
// input. marquee and blink are in here on purpose; center and font too. This
// is meant to feel like 2004.
var allowedElements = map[string]bool{
	// Structure.
	"div": true, "span": true, "p": true, "section": true, "article": true,
	"header": true, "footer": true, "aside": true, "nav": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hgroup": true, "hr": true, "br": true, "wbr": true, "address": true,
	"figure": true, "figcaption": true, "details": true, "summary": true,
	"blockquote": true, "q": true, "cite": true, "pre": true, "code": true,

	// Lists.
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,

	// Tables. People built entire layouts out of these and we are not going
	// to stop them.
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"th": true, "td": true, "caption": true, "colgroup": true, "col": true,

	// Inline text.
	"a": true, "b": true, "strong": true, "i": true, "em": true, "u": true,
	"s": true, "strike": true, "del": true, "ins": true, "small": true,
	"big": true, "sub": true, "sup": true, "mark": true, "abbr": true,
	"dfn": true, "kbd": true, "samp": true, "var": true, "time": true,
	"bdi": true, "bdo": true, "ruby": true, "rt": true, "rp": true,

	// Period-appropriate decoration.
	"marquee": true, "center": true, "font": true, "tt": true, "blink": true,
	"nobr": true,

	// Images. src is restricted to our own origin and small data URIs.
	"img": true, "picture": false,
}

// bannedElementNotices explains the removals people are most likely to try, so
// the editor teaches rather than just deletes.
var bannedElementNotices = map[string]string{
	"script":   "<script> is not allowed on profiles. Nothing on a profile can run code, which is what keeps your visitors safe from each other.",
	"iframe":   "<iframe> is not allowed, so embedded videos and widgets will not work. They would let another website watch everyone who visits you.",
	"object":   "<object> and <embed> are not allowed. Flash is gone and we are not bringing it back.",
	"embed":    "<object> and <embed> are not allowed. Flash is gone and we are not bringing it back.",
	"form":     "<form> and input fields are not allowed. Somebody could use them to build a convincing fake Amici sign-in box on their profile.",
	"input":    "<form> and input fields are not allowed. Somebody could use them to build a convincing fake Amici sign-in box on their profile.",
	"button":   "<button> is not allowed. Use a link styled however you like instead.",
	"textarea": "<form> and input fields are not allowed on profiles.",
	"select":   "<form> and input fields are not allowed on profiles.",
	"style":    "Put your CSS in the stylesheet box rather than a <style> tag, and it will be checked and applied for you.",
	"link":     "<link> is not allowed, so external fonts and stylesheets will not load. Everything a profile shows has to come from Amici.",
	"meta":     "<meta> is not allowed on profiles.",
	"base":     "<base> is not allowed on profiles.",
	"svg":      "Inline <svg> is not allowed, because SVG can carry scripts. The sticker set has decorations you can use instead.",
	"math":     "Inline <math> is not allowed on profiles.",
	"video":    "<video> and <audio> are not allowed. An autoplaying song on every profile was character building, but the file would have to come from somewhere else.",
	"audio":    "<video> and <audio> are not allowed. An autoplaying song on every profile was character building, but the file would have to come from somewhere else.",
	"template": "<template> is not allowed on profiles.",
	"noscript": "<noscript> is not allowed on profiles.",
	"canvas":   "<canvas> is not allowed, because it only does anything with scripts.",
	"dialog":   "<dialog> is not allowed, because it can cover the page.",
	"portal":   "<portal> is not allowed on profiles.",
	"frame":    "Frames are not allowed on profiles.",
	"frameset": "Frames are not allowed on profiles.",
	"applet":   "Applets are not allowed. It is not 1998.",
	"slot":     "<slot> is not allowed on profiles.",
}

// globalAttrs may appear on any allowed element.
var globalAttrs = map[string]bool{
	"class": true, "id": true, "title": true, "lang": true, "dir": true,
	"style": true, "align": true, "hidden": true,
}

// elementAttrs are additional per-element attributes.
var elementAttrs = map[string]map[string]bool{
	"a":          {"href": true, "name": true},
	"img":        {"src": true, "alt": true, "width": true, "height": true, "loading": true, "decoding": true},
	"td":         {"colspan": true, "rowspan": true, "headers": true, "valign": true, "bgcolor": true, "width": true, "height": true},
	"th":         {"colspan": true, "rowspan": true, "scope": true, "valign": true, "bgcolor": true, "width": true, "height": true},
	"tr":         {"valign": true, "bgcolor": true},
	"table":      {"border": true, "cellpadding": true, "cellspacing": true, "width": true, "bgcolor": true, "summary": true},
	"col":        {"span": true, "width": true},
	"colgroup":   {"span": true},
	"ol":         {"start": true, "reversed": true, "type": true},
	"li":         {"value": true},
	"details":    {"open": true},
	"time":       {"datetime": true},
	"blockquote": {"cite": true},
	"q":          {"cite": true},
	"del":        {"cite": true, "datetime": true},
	"ins":        {"cite": true, "datetime": true},
	"font":       {"color": true, "face": true, "size": true},
	"marquee":    {"direction": true, "behavior": true, "scrollamount": true, "scrolldelay": true, "loop": true, "width": true, "height": true, "bgcolor": true, "hspace": true, "vspace": true, "truespeed": true},
	"bdo":        {"dir": true},
}

// numericAttrs must parse as a small non-negative integer, optionally with a
// percent sign, so that width="99999999" cannot be used to wedge a layout.
var numericAttrs = map[string]bool{
	"width": true, "height": true, "colspan": true, "rowspan": true,
	"span": true, "start": true, "value": true, "scrollamount": true, "size": true,
	"scrolldelay": true, "hspace": true, "vspace": true, "border": true,
	"cellpadding": true, "cellspacing": true, "loop": true,
}

var (
	numericAttrRe = regexp.MustCompile(`^-?[0-9]{1,6}%?$`)
	// safeIdentRe covers class and id lists, marquee keywords and similar.
	safeIdentRe = regexp.MustCompile(`^[A-Za-z0-9 _\-]{0,400}$`)
	// safeStyleRe is the character-level check applied by the second pass. It
	// deliberately excludes backslash, angle brackets, braces, semicolon-free
	// obfuscation and the at sign.
	safeStyleRe = regexp.MustCompile(`^[A-Za-z0-9 :;,.%#()'"/*!+_=-]*$`)
	// safeColourRe covers bgcolor and font color.
	safeColourRe = regexp.MustCompile(`^(#[0-9A-Fa-f]{3,8}|[A-Za-z]{1,24})$`)
	// localImageRe restricts img src to paths Amici itself serves. A leading
	// double slash is excluded so //evil.example cannot masquerade as a path.
	localImageRe = regexp.MustCompile(`^/(static|media)/[A-Za-z0-9._/-]{1,180}$`)
	// dataImageRe allows small raster data URIs. SVG is excluded because an
	// SVG document can carry script and its own external references.
	dataImageRe = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]+$`)
)

// Sanitise runs member HTML and CSS through both passes and returns markup
// ready to store and serve.
func Sanitise(htmlSrc, cssSrc string) Result {
	n := newNotices()

	cssOut, cssNotices := Stylesheet(cssSrc)
	for _, m := range cssNotices {
		n.add(m)
	}

	htmlOut := sanitiseHTML(htmlSrc, n)
	htmlOut = secondPass().Sanitize(htmlOut)

	return Result{HTML: htmlOut, CSS: cssOut, Notices: n.list()}
}

// sanitiseHTML is the first pass: parse, walk, rebuild.
//
// Rebuilding from a parsed tree rather than filtering a string is the
// important part. html.Parse gives us the same tree a browser would build,
// including its error recovery, and html.Render writes it back out with
// correct escaping. Nothing here does string surgery on markup.
func sanitiseHTML(src string, n *notices) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	frag, err := html.ParseFragment(strings.NewReader(src), &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	if err != nil {
		n.add("We could not make sense of that HTML, so nothing was saved. Check that every tag you opened is closed.")
		return ""
	}
	st := &walkState{notices: n, budget: MaxElements}
	var out strings.Builder
	for _, node := range frag {
		st.renderNode(&out, node, 0)
	}
	return strings.TrimSpace(out.String())
}

type walkState struct {
	notices     *notices
	budget      int
	depthWarned bool
	textLen     int
}

// renderNode writes a sanitised rendering of node and its children.
func (st *walkState) renderNode(out *strings.Builder, node *html.Node, depth int) {
	switch node.Type {
	case html.TextNode:
		st.textLen += len(node.Data)
		if st.textLen > MaxTextRunLen {
			return
		}
		out.WriteString(html.EscapeString(node.Data))
		return

	case html.CommentNode:
		// Comments are dropped rather than preserved. Conditional comments
		// were a real script vector in old browsers and a comment can never
		// be the reason a profile looks right.
		return

	case html.DoctypeNode, html.DocumentNode:
		st.renderChildren(out, node, depth)
		return

	case html.ElementNode:
		// handled below

	default:
		return
	}

	name := strings.ToLower(node.Data)

	if !allowedElements[name] {
		if msg, ok := bannedElementNotices[name]; ok {
			st.notices.add(msg)
		} else {
			st.notices.add(fmt.Sprintf("The <%s> tag is not on the profile allowlist, so it was removed. Its contents were kept.", truncate(name, 20)))
		}
		// Unwrap rather than delete: keeping the text means a member who
		// wrapped their life story in an unsupported tag does not lose it.
		// Elements that could carry code are dropped whole instead.
		if dropsContent(name) {
			return
		}
		st.renderChildren(out, node, depth)
		return
	}

	if st.budget <= 0 {
		return
	}
	st.budget--

	if depth >= MaxDepth {
		if !st.depthWarned {
			st.notices.add(fmt.Sprintf("Your HTML nests more than %d levels deep. The deepest parts were removed.", MaxDepth))
			st.depthWarned = true
		}
		return
	}

	attrs, keep := st.cleanAttrs(name, node.Attr)
	if !keep {
		return
	}

	out.WriteByte('<')
	out.WriteString(name)
	for _, a := range attrs {
		out.WriteByte(' ')
		out.WriteString(a.Key)
		out.WriteString(`="`)
		out.WriteString(html.EscapeString(a.Val))
		out.WriteByte('"')
	}
	if voidElements[name] {
		out.WriteString(" />")
		return
	}
	out.WriteByte('>')
	st.renderChildren(out, node, depth+1)
	out.WriteString("</")
	out.WriteString(name)
	out.WriteByte('>')
}

func (st *walkState) renderChildren(out *strings.Builder, node *html.Node, depth int) {
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		st.renderNode(out, c, depth)
	}
}

// dropsContent lists elements whose children are not markup to be recovered.
// The text inside a <script> is code, and unwrapping it would paste that code
// into the document as visible text at best.
func dropsContent(name string) bool {
	switch name {
	case "script", "style", "noscript", "template", "svg", "math", "iframe",
		"object", "embed", "applet", "frame", "frameset", "canvas", "portal",
		"textarea", "select", "option", "head", "title":
		return true
	}
	return false
}

var voidElements = map[string]bool{
	"br": true, "hr": true, "img": true, "wbr": true, "col": true,
}

// cleanAttrs applies the attribute allowlist and per-attribute value rules.
// The boolean reports whether the element should be kept at all: an image
// whose source was rejected is not worth rendering as a broken box.
func (st *walkState) cleanAttrs(element string, attrs []html.Attribute) ([]html.Attribute, bool) {
	out := make([]html.Attribute, 0, len(attrs))
	seen := map[string]bool{}
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		if a.Namespace != "" {
			continue
		}
		if seen[key] {
			continue
		}
		if strings.HasPrefix(key, "on") {
			st.notices.add("Event handler attributes like onclick were removed. Profiles cannot run code.")
			continue
		}
		if strings.HasPrefix(key, "data-") || strings.HasPrefix(key, "aria-") {
			// These are inert, but they are also only useful to scripts and
			// assistive technology we are not driving, so they add surface
			// for no benefit.
			continue
		}
		if !globalAttrs[key] && !elementAttrs[element][key] {
			continue
		}
		if len(a.Val) > MaxAttrValueLen && key != "style" && key != "src" {
			continue
		}
		val, ok := st.cleanAttrValue(element, key, a.Val)
		if !ok {
			continue
		}
		seen[key] = true
		out = append(out, html.Attribute{Key: key, Val: val})
	}
	// Links get their safety attributes forced on, every time, regardless of
	// what the member wrote. Note that target and rel are not in the allowlist
	// above, so a member-supplied value has already been discarded by here.
	if element == "a" && seen["href"] {
		out = append(out,
			html.Attribute{Key: "target", Val: "_blank"},
			html.Attribute{Key: "rel", Val: "noopener noreferrer nofollow ugc external"},
		)
	}
	if element == "img" {
		if !seen["src"] {
			// An image whose source we rejected is not worth a broken box.
			return nil, false
		}
		if !seen["loading"] {
			out = append(out, html.Attribute{Key: "loading", Val: "lazy"})
		}
	}
	return out, true
}

func (st *walkState) cleanAttrValue(element, key, val string) (string, bool) {
	val = strings.TrimSpace(val)
	switch {
	case key == "style":
		clean, _ := Declarations(val, st.notices)
		if clean == "" {
			return "", false
		}
		if len(clean) > 4000 {
			return "", false
		}
		return clean, true

	case key == "href":
		return st.cleanHref(val)

	case key == "src":
		return st.cleanImageSrc(val)

	case key == "cite":
		return st.cleanHref(val)

	case numericAttrs[key]:
		if !numericAttrRe.MatchString(val) {
			return "", false
		}
		return val, true

	case key == "bgcolor" || key == "color":
		if !safeColourRe.MatchString(val) {
			return "", false
		}
		return val, true

	case key == "class" || key == "id" || key == "face":
		if !safeIdentRe.MatchString(val) {
			st.notices.add("A class or id used characters we do not allow and was removed. Stick to letters, numbers, dashes and underscores.")
			return "", false
		}
		return val, true

	case key == "hidden" || key == "open" || key == "reversed" || key == "truespeed":
		return "", true

	default:
		// Remaining allowlisted attributes are plain text: alt, title, lang,
		// dir, align, datetime, summary and friends. Escaping at render time
		// makes their content harmless; we only bound the length.
		if len(val) > MaxAttrValueLen {
			val = val[:MaxAttrValueLen]
		}
		return val, true
	}
}

// cleanHref restricts link targets to schemes that navigate, and only ever in
// a new tab with the referrer suppressed.
func (st *walkState) cleanHref(val string) (string, bool) {
	v := strings.TrimSpace(val)
	// Control characters and whitespace inside a URL are the classic way to
	// smuggle "java\nscript:" past a prefix check.
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if v == "" {
		return "", false
	}
	lower := strings.ToLower(v)
	switch {
	case strings.HasPrefix(v, "#"):
		if !safeIdentRe.MatchString(strings.TrimPrefix(v, "#")) {
			return "", false
		}
		return v, true
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if len(v) > 1000 {
			return "", false
		}
		return v, true
	case strings.HasPrefix(lower, "mailto:"):
		if len(v) > 320 {
			return "", false
		}
		return v, true
	case strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//"):
		if len(v) > 300 {
			return "", false
		}
		return v, true
	default:
		st.notices.add("A link was removed because it did not use http, https or mailto. Relative links have to stay inside Amici.")
		return "", false
	}
}

// cleanImageSrc restricts images to files Amici serves and small inline data
// URIs, so that viewing a profile never contacts a third party.
func (st *walkState) cleanImageSrc(val string) (string, bool) {
	v := strings.TrimSpace(val)
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if localImageRe.MatchString(v) {
		return v, true
	}
	if strings.HasPrefix(strings.ToLower(v), "data:") {
		if len(v) > MaxDataURILen {
			st.notices.add("An inline image was larger than we allow and was removed.")
			return "", false
		}
		if !dataImageRe.MatchString(v) {
			st.notices.add("An inline image was removed because it was not a plain PNG, JPEG, GIF or WebP. SVG is not allowed because it can carry scripts.")
			return "", false
		}
		return v, true
	}
	st.notices.add("An image pointing at another website was removed. Profile images have to come from Amici, because loading one from elsewhere would tell that website the address of everyone who visits you. Have a look at the stickers.")
	return "", false
}

// secondPass builds the independent bluemonday policy.
//
// It is intentionally written from the allowlists above rather than being a
// loose "allow anything the first pass emits" policy. If the two passes
// disagree the stricter one wins, which is the point of having two.
//
// Note that AllowStyles is deliberately never called. Doing so would route
// the style attribute through bluemonday's own CSS parser, which validates a
// normalised copy of a value but emits the original, so a CSS escape could
// survive. Instead style is treated as an ordinary attribute and checked
// character by character against safeStyleRe, on the exact bytes that will be
// written out.
func secondPass() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	names := make([]string, 0, len(allowedElements))
	for name, ok := range allowedElements {
		if ok {
			names = append(names, name)
		}
	}
	p.AllowElements(names...)
	// bluemonday drops an allowed element that ends up with no attributes
	// unless it is on this list. Its built-in list covers standard HTML, which
	// leaves exactly the tags we care most about keeping: a bare <blink> or
	// <marquee> would quietly vanish.
	p.AllowNoAttrs().OnElements(names...)

	p.AllowAttrs("class", "id").Matching(safeIdentRe).Globally()
	p.AllowAttrs("title", "lang", "dir", "align").Globally()
	p.AllowAttrs("style").Matching(safeStyleRe).Globally()

	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("target", "rel", "name").OnElements("a")
	p.AllowAttrs("src").Matching(regexp.MustCompile(
		localImageRe.String() + `|` + dataImageRe.String(),
	)).OnElements("img")
	p.AllowAttrs("alt", "loading", "decoding").OnElements("img")
	p.AllowAttrs("width", "height", "colspan", "rowspan", "span", "start",
		"value", "scrollamount", "scrolldelay", "hspace", "vspace", "border",
		"cellpadding", "cellspacing", "loop").Matching(numericAttrRe).Globally()
	p.AllowAttrs("bgcolor", "color").Matching(safeColourRe).Globally()
	p.AllowAttrs("face").Matching(safeIdentRe).OnElements("font")
	p.AllowAttrs("size").Matching(numericAttrRe).OnElements("font")
	p.AllowAttrs("direction", "behavior", "truespeed").OnElements("marquee")
	p.AllowAttrs("scope", "headers", "valign").OnElements("th", "td")
	p.AllowAttrs("valign").OnElements("tr")
	p.AllowAttrs("summary").OnElements("table")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("datetime").OnElements("time", "del", "ins")
	p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
	p.AllowAttrs("reversed", "type").OnElements("ol")

	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	// AllowDataURIImages adds the data scheme with a base64 validator. Its own
	// prefix check permits image/svg+xml, which we do not want, but the src
	// attribute policy above runs first and its regex excludes SVG, so both
	// have to agree before an inline image survives.
	p.AllowDataURIImages()
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	return p
}
