package domain

import "time"

// CanvasHTMLMaxLen and CanvasCSSMaxLen bound how much markup a member can
// store. Generous enough for a proper mid-2000s shrine, small enough that a
// profile cannot be used as free file hosting or as a memory exhaustion
// vector.
const (
	CanvasHTMLMaxLen = 24 * 1024
	CanvasCSSMaxLen  = 12 * 1024
)

// Canvas is a member's hand-written profile page: the MySpace bit.
//
// Two copies of the markup are kept. Source is exactly what the member typed,
// so the editor can show it back to them unchanged. Rendered is the result of
// running Source through the sanitiser, and it is the only version ever sent
// to a browser. Sanitising once on save rather than on every view means a
// profile view is a cheap read, and it also means a change to the sanitiser
// can be replayed over stored sources as a migration.
type Canvas struct {
	AccountID    ID
	HTMLSource   string
	CSSSource    string
	HTMLRendered string
	CSSRendered  string
	// SanitiserVersion records which sanitiser produced Rendered so that a
	// future tightening of the rules can find and re-render stale canvases.
	SanitiserVersion int
	// Notices are human-readable descriptions of what the sanitiser removed,
	// shown to the member after saving so the rules are never a mystery.
	//
	// They belong to one save and are not stored. A canvas read back from the
	// database always has none, which is deliberate: notices are an
	// explanation of something that just happened, and showing somebody a
	// list of complaints about markup they fixed a month ago would be worse
	// than saying nothing.
	Notices   []string
	UpdatedAt time.Time
}

// Empty reports whether there is anything to render.
func (c *Canvas) Empty() bool { return c == nil || (c.HTMLRendered == "" && c.CSSRendered == "") }
