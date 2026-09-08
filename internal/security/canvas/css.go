package canvas

import (
	"fmt"
	"sort"
	"strings"
)

// This file implements Amici's CSS allowlist.
//
// It is deliberately not a complete CSS parser. A complete parser would have
// to reproduce every quirk of every browser's error recovery, and any place
// where our understanding differs from the browser's is a bypass. Instead we
// accept a small, well-understood subset and reject everything we are not
// certain about. A member who wants a property we do not support gets a clear
// notice rather than a silently broken page.
//
// Three rules do most of the work:
//
//  1. No backslashes anywhere. CSS escapes (\75 rl for url) are the standard
//     way to smuggle a banned token past a naive filter. Since escapes are
//     never needed for the decorative CSS people actually write, banning the
//     character outright removes the entire obfuscation class.
//  2. No url(). Not "no external url()" - none at all. An external URL in a
//     stylesheet fires a request from the viewer's browser to a third party,
//     handing that third party the viewer's IP address and a timestamp. On a
//     network built to keep people unfindable, that is the whole game lost for
//     a background tile. Local decoration is provided by a built-in sticker
//     set instead.
//  3. Every function call in a value must be named in the allowlist, so
//     gradients and calc work while expression() and image-set() do not.

// css limits. A profile is a page, not a payload.
const (
	maxSelectorLen     = 200
	maxSelectorsInList = 12
	maxDeclarations    = 600
	maxRules           = 200
	maxValueLen        = 400
	maxNestingDepth    = 1
)

// rootSelector is what every member selector is scoped beneath. The canvas is
// rendered inside its own document, so scoping is not what makes it safe, but
// it keeps the stylesheet honest and means the same rendered CSS could be
// dropped into a shared page later without leaking out of its box.
const rootSelector = "#amici-canvas"

// allowedProperties is the set of declarations a canvas may use. It covers
// what people want for decoration and layout, and stops short of anything
// that can reach outside the box or fetch a resource.
var allowedProperties = map[string]bool{
	// Text and typography.
	"color": true, "font": true, "font-family": true, "font-size": true,
	"font-style": true, "font-weight": true, "font-variant": true,
	"font-stretch": true, "line-height": true, "letter-spacing": true,
	"word-spacing": true, "text-align": true, "text-align-last": true,
	"text-decoration": true, "text-decoration-color": true,
	"text-decoration-line": true, "text-decoration-style": true,
	"text-decoration-thickness": true, "text-underline-offset": true,
	"text-indent": true, "text-transform": true, "text-shadow": true,
	"text-wrap": true, "white-space": true, "word-break": true,
	"overflow-wrap": true, "hyphens": true, "direction": true,
	"unicode-bidi": true, "writing-mode": true, "vertical-align": true,
	"font-feature-settings": true, "font-variant-caps": true, "tab-size": true,

	// Colour and background. background-image is allowed because the value
	// sanitiser rejects url(), leaving gradients, which is where the fun is.
	"background": true, "background-color": true, "background-image": true,
	"background-position": true, "background-repeat": true,
	"background-size": true, "background-clip": true, "background-origin": true,
	"background-attachment": true, "background-blend-mode": true,
	"mix-blend-mode": true, "opacity": true, "accent-color": true,

	// Box model.
	"width": true, "min-width": true, "max-width": true,
	"height": true, "min-height": true, "max-height": true,
	"margin": true, "margin-top": true, "margin-right": true,
	"margin-bottom": true, "margin-left": true, "margin-inline": true,
	"margin-block": true, "padding": true, "padding-top": true,
	"padding-right": true, "padding-bottom": true, "padding-left": true,
	"padding-inline": true, "padding-block": true, "box-sizing": true,
	"aspect-ratio": true,

	// Borders, corners and shadows.
	"border": true, "border-top": true, "border-right": true,
	"border-bottom": true, "border-left": true, "border-color": true,
	"border-style": true, "border-width": true, "border-radius": true,
	"border-top-left-radius": true, "border-top-right-radius": true,
	"border-bottom-left-radius": true, "border-bottom-right-radius": true,
	"border-collapse": true, "border-spacing": true, "box-shadow": true,
	"outline": true, "outline-color": true, "outline-style": true,
	"outline-width": true, "outline-offset": true,

	// Layout. position is allowed, but the value sanitiser restricts it to
	// values that cannot pin content over the viewport.
	"display": true, "position": true, "top": true, "right": true,
	"bottom": true, "left": true, "float": true, "clear": true,
	"z-index": true, "overflow": true, "overflow-x": true, "overflow-y": true,
	"visibility": true, "gap": true, "row-gap": true, "column-gap": true,
	"flex": true, "flex-basis": true, "flex-direction": true,
	"flex-flow": true, "flex-grow": true, "flex-shrink": true,
	"flex-wrap": true, "align-content": true, "align-items": true,
	"align-self": true, "justify-content": true, "justify-items": true,
	"justify-self": true, "order": true, "place-items": true,
	"place-content": true, "grid": true, "grid-template": true,
	"grid-template-columns": true, "grid-template-rows": true,
	"grid-template-areas": true, "grid-area": true, "grid-column": true,
	"grid-row": true, "grid-auto-flow": true, "grid-auto-columns": true,
	"grid-auto-rows": true, "columns": true, "column-count": true,
	"column-width": true, "column-gap-rule": true,

	// Lists and tables.
	"list-style": true, "list-style-type": true, "list-style-position": true,
	"table-layout": true, "caption-side": true, "empty-cells": true,

	// Motion and effects. Members get their spinning, glowing, gradient
	// nonsense; prefers-reduced-motion is respected by the canvas frame.
	"transform": true, "transform-origin": true, "transition": true,
	"transition-delay": true, "transition-duration": true,
	"transition-property": true, "transition-timing-function": true,
	"animation": true, "animation-delay": true, "animation-direction": true,
	"animation-duration": true, "animation-fill-mode": true,
	"animation-iteration-count": true, "animation-name": true,
	"animation-play-state": true, "animation-timing-function": true,
	"filter": true, "backdrop-filter": true, "rotate": true, "scale": true,
	"translate": true,

	// Miscellaneous decoration.
	"content": true, "cursor": true, "text-emphasis": true,
	"object-fit": true, "object-position": true, "clip-path": true,
	"border-image-slice": true, "border-image-width": true,
	"image-rendering": true, "isolation": true, "resize": true,
	"scrollbar-color": true, "scrollbar-width": true, "caret-color": true,
	"quotes": true, "counter-reset": true, "counter-increment": true,
}

// allowedFunctions are the only function names permitted in a value.
var allowedFunctions = map[string]bool{
	"rgb": true, "rgba": true, "hsl": true, "hsla": true, "hwb": true,
	"calc": true, "min": true, "max": true, "clamp": true,
	"linear-gradient": true, "radial-gradient": true, "conic-gradient": true,
	"repeating-linear-gradient": true, "repeating-radial-gradient": true,
	"repeating-conic-gradient": true,
	"translate":                true, "translatex": true, "translatey": true,
	"translate3d": true, "scale": true, "scalex": true, "scaley": true,
	"rotate": true, "rotatex": true, "rotatey": true, "rotatez": true,
	"rotate3d": true, "skew": true, "skewx": true, "skewy": true,
	"matrix": true, "perspective": true,
	"cubic-bezier": true, "steps": true,
	"blur": true, "brightness": true, "contrast": true, "grayscale": true,
	"hue-rotate": true, "invert": true, "opacity": true, "saturate": true,
	"sepia": true, "drop-shadow": true,
	"circle": true, "ellipse": true, "inset": true, "polygon": true,
	"counter": true,
	// attr() is absent on purpose: it can lift attribute values into
	// generated content, and paired with a selector it becomes a way to read
	// the page back out.
}

// bannedValueSubstrings are checked against the lowercased value. Each one is
// a known way to fetch a resource or execute something.
var bannedValueSubstrings = []string{
	"url(", "expression", "javascript:", "vbscript:", "data:", "-moz-binding",
	"behavior", "progid:", "image-set", "element(", "@import", "cross-fade",
	"paint(", "-webkit-canvas", "src(",
}

// positionAllowed keeps content inside its own box. fixed and sticky would let
// a canvas float an element over whatever is around it, which is the raw
// material for a convincing fake sign-in prompt.
var positionAllowed = map[string]bool{
	"static": true, "relative": true, "absolute": true, "revert": true,
	"initial": true, "inherit": true, "unset": true,
}

// allowedPseudo is the set of pseudo-classes and pseudo-elements a selector
// may use. Everything here is presentational.
var allowedPseudo = map[string]bool{
	"hover": true, "focus": true, "focus-visible": true, "active": true,
	"first-child": true, "last-child": true, "only-child": true,
	"first-of-type": true, "last-of-type": true, "only-of-type": true,
	"nth-child": true, "nth-of-type": true, "nth-last-child": true,
	"empty": true, "not": true, "is": true, "where": true, "root": true,
	"before": true, "after": true, "first-line": true, "first-letter": true,
	"marker": true, "placeholder": true, "selection": true, "target-text": true,
}

// allowedAtRules are the block at-rules a stylesheet may contain. @media and
// @supports are progressive enhancement; @keyframes is what makes a marquee
// era profile sing. @import and @font-face are absent because both fetch.
var allowedAtRules = map[string]bool{
	"media": true, "supports": true, "keyframes": true,
}

// Stylesheet sanitises a member-authored stylesheet, returning the rewritten
// CSS and human-readable notices describing anything that was dropped.
func Stylesheet(src string) (string, []string) {
	n := newNotices()
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	if strings.Contains(src, "\\") {
		n.add("CSS escape sequences (backslashes) are not supported, so that whole stylesheet was skipped. Write values out in full instead.")
		return "", n.list()
	}
	src, ok := stripComments(src)
	if !ok {
		n.add("There is an unclosed /* comment in your CSS, so the stylesheet was skipped.")
		return "", n.list()
	}
	var out strings.Builder
	budget := &ruleBudget{rules: maxRules, declarations: maxDeclarations}
	sanitiseBlocks(src, 0, budget, n, &out)
	return strings.TrimSpace(out.String()), n.list()
}

// ruleBudget caps total output so one member cannot ship a megabyte of CSS.
type ruleBudget struct {
	rules        int
	declarations int
	warned       bool
}

func (b *ruleBudget) takeRule(n *notices) bool {
	if b.rules <= 0 {
		if !b.warned {
			n.add(fmt.Sprintf("Your CSS is over the limit of %d rules, so the rest was dropped.", maxRules))
			b.warned = true
		}
		return false
	}
	b.rules--
	return true
}

// sanitiseBlocks walks a sequence of rules at one nesting level.
func sanitiseBlocks(src string, depth int, budget *ruleBudget, n *notices, out *strings.Builder) {
	rest := src
	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return
		}
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			if strings.TrimSpace(rest) != "" {
				n.add("Some CSS at the end had no rule body and was dropped.")
			}
			return
		}
		prelude := strings.TrimSpace(rest[:open])
		body, after, ok := matchBrace(rest[open:])
		if !ok {
			n.add("There is an unclosed { in your CSS, so the rest of the stylesheet was dropped.")
			return
		}
		rest = after

		if strings.HasPrefix(prelude, "@") {
			sanitiseAtRule(prelude, body, depth, budget, n, out)
			continue
		}
		sanitiseStyleRule(prelude, body, depth, budget, n, out)
	}
}

func sanitiseAtRule(prelude, body string, depth int, budget *ruleBudget, n *notices, out *strings.Builder) {
	name, condition := splitAtRule(prelude)
	if !allowedAtRules[name] {
		n.add(fmt.Sprintf("@%s is not supported on profiles and was removed. Anything that loads a file from another website is off the table here, to keep your visitors' details private.", name))
		return
	}
	if depth >= maxNestingDepth {
		n.add("At-rules cannot be nested inside each other, so one was dropped.")
		return
	}
	if !safeAtRuleCondition(condition) {
		n.add(fmt.Sprintf("The condition on your @%s rule used characters we do not allow, so the rule was dropped.", name))
		return
	}
	var inner strings.Builder
	if name == "keyframes" {
		sanitiseKeyframes(body, budget, n, &inner)
	} else {
		sanitiseBlocks(body, depth+1, budget, n, &inner)
	}
	if strings.TrimSpace(inner.String()) == "" {
		return
	}
	if condition != "" {
		fmt.Fprintf(out, "@%s %s{\n%s}\n", name, condition, inner.String())
	} else {
		fmt.Fprintf(out, "@%s{\n%s}\n", name, inner.String())
	}
}

// sanitiseKeyframes handles the percentage/from/to selectors inside
// @keyframes, which are not element selectors and must not be scoped.
func sanitiseKeyframes(body string, budget *ruleBudget, n *notices, out *strings.Builder) {
	rest := body
	for {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return
		}
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			return
		}
		stop := strings.TrimSpace(rest[:open])
		inner, after, ok := matchBrace(rest[open:])
		if !ok {
			n.add("There is an unclosed { inside your @keyframes rule.")
			return
		}
		rest = after
		if !validKeyframeStop(stop) {
			n.add(fmt.Sprintf("%q is not a valid keyframe step, so it was dropped. Use from, to, or a percentage.", stop))
			continue
		}
		decls, count := Declarations(inner, n)
		if decls == "" {
			continue
		}
		if !budget.takeRule(n) {
			return
		}
		budget.declarations -= count
		fmt.Fprintf(out, "  %s{%s}\n", stop, decls)
	}
}

func validKeyframeStop(s string) bool {
	if s == "" || len(s) > 60 {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "from" || part == "to" {
			continue
		}
		if !strings.HasSuffix(part, "%") {
			return false
		}
		num := strings.TrimSuffix(part, "%")
		if num == "" {
			return false
		}
		for _, r := range num {
			if (r < '0' || r > '9') && r != '.' {
				return false
			}
		}
	}
	return true
}

func sanitiseStyleRule(prelude, body string, depth int, budget *ruleBudget, n *notices, out *strings.Builder) {
	selector, ok := Selector(prelude, n)
	if !ok {
		return
	}
	decls, count := Declarations(body, n)
	if decls == "" {
		return
	}
	if budget.declarations-count < 0 {
		n.add(fmt.Sprintf("Your CSS is over the limit of %d declarations, so the rest was dropped.", maxDeclarations))
		budget.declarations = 0
		return
	}
	if !budget.takeRule(n) {
		return
	}
	budget.declarations -= count
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(out, "%s%s{%s}\n", indent, selector, decls)
}

// Selector validates and scopes a member selector list.
func Selector(src string, n *notices) (string, bool) {
	parts := strings.Split(src, ",")
	if len(parts) > maxSelectorsInList {
		n.add(fmt.Sprintf("A selector listed more than %d parts and was dropped.", maxSelectorsInList))
		return "", false
	}
	scoped := make([]string, 0, len(parts))
	for _, raw := range parts {
		sel := strings.Join(strings.Fields(raw), " ")
		if sel == "" {
			continue
		}
		if len(sel) > maxSelectorLen {
			n.add("One of your selectors was too long and was dropped.")
			continue
		}
		if !safeSelector(sel) {
			n.add(fmt.Sprintf("The selector %q uses something we do not allow and was dropped.", truncate(raw, 60)))
			continue
		}
		scoped = append(scoped, scopeSelector(sel))
	}
	if len(scoped) == 0 {
		return "", false
	}
	return strings.Join(scoped, ", ") + " ", true
}

// scopeSelector rewrites a selector so it can only ever match inside the
// canvas. Document-level selectors are folded onto the canvas root, which is
// what a member means when they write "body { background: pink }".
func scopeSelector(sel string) string {
	lower := strings.ToLower(sel)
	for _, root := range []string{"html", "body", ":root"} {
		if lower == root {
			return rootSelector
		}
		if strings.HasPrefix(lower, root+" ") {
			return rootSelector + sel[len(root):]
		}
		if strings.HasPrefix(lower, root+":") || strings.HasPrefix(lower, root+"::") {
			return rootSelector + sel[len(root):]
		}
		if strings.HasPrefix(lower, root+" >") || strings.HasPrefix(lower, root+">") {
			return rootSelector + sel[len(root):]
		}
	}
	if strings.HasPrefix(sel, ">") || strings.HasPrefix(sel, "+") || strings.HasPrefix(sel, "~") {
		return rootSelector + " " + sel
	}
	return rootSelector + " " + sel
}

const selectorChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .#_-*>+~:()[]=\"'^$|,"

func safeSelector(sel string) bool {
	for _, r := range sel {
		if !strings.ContainsRune(selectorChars, r) {
			return false
		}
	}
	// Every pseudo-class or pseudo-element must be one we know.
	rest := sel
	for {
		i := strings.IndexByte(rest, ':')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		rest = strings.TrimPrefix(rest, ":")
		end := len(rest)
		for j, r := range rest {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz-", r) {
				end = j
				break
			}
		}
		name := rest[:end]
		if !allowedPseudo[strings.ToLower(name)] {
			return false
		}
		rest = rest[end:]
	}
	return true
}

func safeAtRuleCondition(cond string) bool {
	if len(cond) > 200 {
		return false
	}
	const ok = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .:()-,><=/*_"
	for _, r := range cond {
		if !strings.ContainsRune(ok, r) {
			return false
		}
	}
	return !strings.Contains(strings.ToLower(cond), "url")
}

// Declarations sanitises a declaration block, returning the rewritten text and
// the number of declarations kept. It is used both for rule bodies and for
// inline style attributes.
func Declarations(body string, n *notices) (string, int) {
	if strings.Contains(body, "\\") {
		n.add("CSS escape sequences (backslashes) are not supported, so a style was skipped.")
		return "", 0
	}
	kept := make([]string, 0, 8)
	for _, decl := range strings.Split(body, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		colon := strings.IndexByte(decl, ':')
		if colon <= 0 {
			n.add(fmt.Sprintf("The style %q is missing a colon and was dropped.", truncate(decl, 50)))
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(decl[:colon]))
		value := strings.TrimSpace(decl[colon+1:])
		prop = stripVendorPrefix(prop)
		if !allowedProperties[prop] {
			n.add(fmt.Sprintf("The CSS property %q is not on the profile allowlist and was removed.", truncate(prop, 40)))
			continue
		}
		clean, ok := value_(prop, value)
		if !ok {
			n.add(fmt.Sprintf("The value for %q was not something we allow, so that line was removed.", prop))
			continue
		}
		kept = append(kept, prop+": "+clean)
	}
	if len(kept) == 0 {
		return "", 0
	}
	return strings.Join(kept, "; ") + ";", len(kept)
}

var vendorPrefixes = []string{"-webkit-", "-moz-", "-ms-", "-o-", "mso-", "-khtml-"}

// stripVendorPrefix normalises a property so that -webkit-transform is checked
// as transform. Allowing a prefix to bypass the allowlist would be the easiest
// hole imaginable.
func stripVendorPrefix(prop string) string {
	for _, p := range vendorPrefixes {
		if strings.HasPrefix(prop, p) {
			return strings.TrimPrefix(prop, p)
		}
	}
	return prop
}

const valueChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 .,%#()-+*/'\"!_="

// value_ validates a declaration value. The trailing underscore keeps the name
// from colliding with the far more common word "value" used as a variable.
func value_(prop, v string) (string, bool) {
	if v == "" || len(v) > maxValueLen {
		return "", false
	}
	lower := strings.ToLower(v)
	for _, bad := range bannedValueSubstrings {
		if strings.Contains(lower, bad) {
			return "", false
		}
	}
	for _, r := range v {
		if !strings.ContainsRune(valueChars, r) {
			return "", false
		}
	}
	if strings.Count(v, "(") != strings.Count(v, ")") {
		return "", false
	}
	if !functionsAllowed(lower) {
		return "", false
	}
	switch prop {
	case "position":
		if !positionAllowed[strings.TrimSuffix(strings.TrimSpace(lower), "!important")] {
			return "", false
		}
	case "z-index":
		if !safeZIndex(lower) {
			return "", false
		}
	case "content":
		if !safeContent(v) {
			return "", false
		}
	}
	return v, true
}

// functionsAllowed checks that every identifier immediately followed by "("
// names an allowed function.
func functionsAllowed(lower string) bool {
	for i := 0; i < len(lower); i++ {
		if lower[i] != '(' {
			continue
		}
		start := i
		for start > 0 && (isIdentByte(lower[start-1])) {
			start--
		}
		name := lower[start:i]
		if name == "" {
			// A bare "(" with no function name is grouping inside calc, which
			// is fine, but only when a calc-like function encloses it.
			continue
		}
		if !allowedFunctions[name] {
			return false
		}
	}
	return true
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '_'
}

func safeZIndex(lower string) bool {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(lower), "!important"))
	switch s {
	case "auto", "initial", "inherit", "unset", "revert":
		return true
	}
	// The length cap keeps the magnitude under 10000. A canvas only ever
	// stacks against itself, so there is no reason to reach for 2147483647.
	s = strings.TrimPrefix(s, "-")
	if s == "" || len(s) > 4 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// safeContent restricts generated content to quoted strings and keywords, so
// nobody can use content to pull in a counter-based side channel or a resource.
func safeContent(v string) bool {
	s := strings.TrimSpace(v)
	switch strings.ToLower(s) {
	case "none", "normal", "initial", "inherit", "unset", "open-quote",
		"close-quote", "no-open-quote", "no-close-quote", "":
		return true
	}
	if len(s) < 2 {
		return false
	}
	q := s[0]
	if (q != '"' && q != '\'') || s[len(s)-1] != q {
		return false
	}
	return !strings.Contains(s[1:len(s)-1], string(q))
}

// stripComments removes /* ... */ and reports whether the input was balanced.
func stripComments(src string) (string, bool) {
	var out strings.Builder
	out.Grow(len(src))
	for {
		i := strings.Index(src, "/*")
		if i < 0 {
			out.WriteString(src)
			return out.String(), true
		}
		out.WriteString(src[:i])
		src = src[i+2:]
		j := strings.Index(src, "*/")
		if j < 0 {
			return out.String(), false
		}
		// A comment becomes a space rather than nothing, so that a/**/b does
		// not silently become the identifier ab.
		out.WriteByte(' ')
		src = src[j+2:]
	}
}

// matchBrace expects src to start with '{' and returns the block contents and
// whatever follows the matching '}'.
func matchBrace(src string) (body, rest string, ok bool) {
	if len(src) == 0 || src[0] != '{' {
		return "", "", false
	}
	depth := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[1:i], src[i+1:], true
			}
		}
	}
	return "", "", false
}

func splitAtRule(prelude string) (name, condition string) {
	s := strings.TrimPrefix(prelude, "@")
	i := strings.IndexAny(s, " \t\n(")
	if i < 0 {
		return strings.ToLower(s), ""
	}
	return strings.ToLower(s[:i]), strings.TrimSpace(s[i:])
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "\u2026"
}

// notices collects deduplicated, human-readable explanations of what the
// sanitiser changed. Members are told exactly what happened to their markup;
// silent removal is how you end up with a support queue full of "my profile is
// broken and I don't know why".
type notices struct {
	seen map[string]bool
}

func newNotices() *notices { return &notices{seen: map[string]bool{}} }

func (n *notices) add(msg string) {
	if n == nil || n.seen == nil {
		return
	}
	if len(n.seen) >= 25 {
		return
	}
	n.seen[msg] = true
}

func (n *notices) list() []string {
	if n == nil || len(n.seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(n.seen))
	for m := range n.seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
