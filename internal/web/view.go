package web

import (
	"github.com/kurtisrogers/amici/internal/domain"
)

// itemView is a feed item plus the context needed to render the controls
// around it.
//
// A Go template can only be invoked with a single value, and the post partial
// needs four things: the item, who is looking at it, the CSRF token for its
// forms, and where to send the browser afterwards. Bundling them in a typed
// struct built by a helper keeps the partial readable and keeps the compiler
// involved. The alternative found in most Go codebases is a "dict" template
// function, which moves the same problem into a place where a typo is a
// runtime error on somebody's feed instead of a build failure.
type itemView struct {
	Item   domain.FeedItem
	Viewer *domain.Account
	CSRF   string
	// ReturnTo is where a reaction or a comment sends the browser back to, so
	// that acting on a post from a profile does not dump you on the feed.
	ReturnTo string
	// Expanded renders the whole conversation rather than the first few
	// comments and a link.
	Expanded bool
}

// Mine reports whether the viewer wrote the post, which decides whether they
// are offered delete or report.
func (v itemView) Mine() bool {
	return v.Viewer != nil && v.Viewer.ID == v.Item.Post.AuthorID
}

// CanDelete reports whether the viewer may remove a comment: their own, or
// anybody's on a post of theirs. It mirrors the rule the feed service
// enforces, and the service is the one that actually decides.
func (v itemView) CanDelete(c domain.CommentView) bool {
	return v.Viewer != nil && (v.Viewer.ID == c.Comment.AuthorID || v.Mine())
}

// item builds an itemView from the page it is being rendered on.
func item(p *page, it domain.FeedItem, returnTo string, expanded bool) itemView {
	v := itemView{Item: it, ReturnTo: returnTo, Expanded: expanded}
	if p != nil {
		v.Viewer = p.Viewer
		v.CSRF = p.CSRF
	}
	return v
}
