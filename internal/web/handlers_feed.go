package web

import (
	"net/http"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/service"
)

// feedPage is what the feed template renders.
type feedPage struct {
	Page *domain.FeedPage
	// FriendCount decides whether we show the feed or the "it is quiet in
	// here" panel. A new member with no friends should be pointed at how to
	// invite someone, not shown an empty scroll.
	FriendCount     int
	PendingRequests int
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	page, err := s.services.Feed.Home(r.Context(), viewer, r.URL.Query().Get("before"))
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	overview, err := s.services.Friends.Overview(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}

	p := s.newPage(r, "Your friends", "feed", feedPage{
		Page:            page,
		FriendCount:     len(overview.Friends),
		PendingRequests: len(overview.Incoming),
	})
	s.render(w, r, http.StatusOK, "feed.html", p)
}

func (s *Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	_, err := s.services.Feed.Post(r.Context(), viewer, service.NewPost{
		Body:       r.PostFormValue("body"),
		Visibility: r.PostFormValue("visibility"),
	})
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w, "Posted.")
	}
	s.back(w, r, "/feed")
}

// postThreadPage is a single post with its whole conversation.
type postThreadPage struct {
	Item domain.FeedItem
}

func (s *Server) handlePostThread(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	thread, err := s.services.Feed.Thread(r.Context(), viewer, domain.ID(r.PathValue("id")))
	if err != nil {
		s.renderError(w, r, err, http.StatusNotFound)
		return
	}
	p := s.newPage(r, "A post from "+thread.Item.Author.DisplayName, "feed", postThreadPage{Item: thread.Item})
	s.render(w, r, http.StatusOK, "post.html", p)
}

func (s *Server) handleDeletePost(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Feed.DeletePost(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "Deleted.")
	}
	s.back(w, r, "/feed")
}

func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	_, err := s.services.Feed.React(r.Context(), viewer,
		domain.ID(r.PathValue("id")), r.PostFormValue("kind"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	}
	// No flash on success. A reaction is a small gesture and it does not want
	// a banner congratulating you for it.
	s.back(w, r, "/feed")
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	_, err := s.services.Feed.Comment(r.Context(), viewer, service.NewComment{
		PostID: domain.ID(r.PathValue("id")),
		Body:   r.PostFormValue("body"),
	})
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	}
	s.back(w, r, "/feed")
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Feed.DeleteComment(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	}
	s.back(w, r, "/feed")
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Support.Report(r.Context(), viewer, service.NewReport{
		SubjectKind: r.PostFormValue("subject_kind"),
		SubjectID:   domain.ID(r.PostFormValue("subject_id")),
		Reason:      r.PostFormValue("reason"),
	})
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	} else {
		s.flashGood(w,
			"Thank you for telling us.",
			"Somebody in support will read what you wrote. They cannot see your posts or your friends, so your description is what they will be working from.",
		)
	}
	s.back(w, r, "/feed")
}

// profilePage is a member's profile as seen by a viewer.
type profilePage struct {
	Profile *service.Profile
	// CanvasSrc is the URL of the sandboxed frame, empty when there is no
	// canvas to show.
	CanvasSrc string
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	prof, err := s.services.Feed.ProfileFor(r.Context(), viewer, r.PathValue("handle"))
	if err != nil {
		// Every reason for failing here, including "you are not their friend",
		// renders the same page as a handle that was never registered.
		s.renderNotFound(w, r)
		return
	}

	data := profilePage{Profile: prof}
	if !prof.Canvas.Empty() {
		data.CanvasSrc = "/u/" + prof.Account.Handle + "/canvas"
	}
	p := s.newPage(r, prof.Account.DisplayName, "", data)
	s.render(w, r, http.StatusOK, "profile.html", p)
}

// back returns the member to where they came from, or to a fallback.
//
// Post-then-redirect keeps the browser's back button sane and means a refresh
// does not repost. The Referer is filtered through safeRedirect so that a
// crafted header cannot bounce somebody off Amici.
func (s *Server) back(w http.ResponseWriter, r *http.Request, fallback string) {
	if next := safeRedirect(r.PostFormValue("return_to")); next != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fallback, http.StatusSeeOther)
}
