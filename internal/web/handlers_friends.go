package web

import (
	"net/http"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/service"
)

// friendsPage is the friends screen.
type friendsPage struct {
	Overview *service.Overview
	// Minted is the code that was just created, shown once. It is not stored
	// anywhere we can read it back, so if the member navigates away without
	// copying it they need a new one.
	Minted        *service.MintedInvite
	InviteTTL     string
	MaxNoteLength int
}

func (s *Server) handleFriends(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	overview, err := s.services.Friends.Overview(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	p := s.newPage(r, "Friends", "friends", friendsPage{
		Overview:      overview,
		MaxNoteLength: domain.FriendRequestNoteMaxLen,
	})
	s.render(w, r, http.StatusOK, "friends.html", p)
}

// handleRequestByEmail sends a friend request to an email address.
//
// The response is the same whatever happened, because the service is built so
// that the handler genuinely does not know. Note that even the flash message
// is carefully worded: it says what we did, not what we found.
func (s *Server) handleRequestByEmail(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	err := s.services.Friends.RequestByEmail(r.Context(), viewer, service.EmailRequest{
		Email:     r.PostFormValue("email"),
		Note:      r.PostFormValue("note"),
		ClientKey: s.clientKey(r),
	})
	switch {
	case err != nil:
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
	default:
		s.flashGood(w,
			"If that address belongs to somebody on Amici, your request is waiting for them.",
			"We will not tell you either way. Being able to check whether a particular person is here is exactly what we are trying to make impossible, so this message is the same whoever you sent it to.",
		)
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleMintInvite(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	minted, err := s.services.Friends.MintInvite(r.Context(), viewer, r.PostFormValue("label"))
	if err != nil {
		s.flashBad(w, userMessage(err, http.StatusBadRequest))
		http.Redirect(w, r, "/friends", http.StatusSeeOther)
		return
	}

	overview, err := s.services.Friends.Overview(r.Context(), viewer)
	if err != nil {
		s.renderError(w, r, err, http.StatusInternalServerError)
		return
	}
	// Rendered rather than redirected, because the code itself only exists in
	// memory for this one response. A redirect would mean either putting it in
	// a URL or storing it somewhere retrievable, and neither is acceptable for
	// a secret we promised only to keep as a hash.
	p := s.newPage(r, "Friends", "friends", friendsPage{
		Overview:      overview,
		Minted:        minted,
		MaxNoteLength: domain.FriendRequestNoteMaxLen,
	})
	s.render(w, r, http.StatusOK, "friends.html", p)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.RevokeInvite(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "That code will not work any more.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

// redeemPage is the form for entering a request identifier.
type redeemPage struct {
	Code  string
	Error string
}

func (s *Server) handleRedeemForm(w http.ResponseWriter, r *http.Request) {
	// A code may arrive in the query string because the member followed a
	// link. It is pre-filled but never submitted automatically: redeeming is
	// a state change and must not happen on a GET.
	p := s.newPage(r, "Use a request code", "friends", redeemPage{
		Code: r.URL.Query().Get("code"),
	})
	s.render(w, r, http.StatusOK, "redeem.html", p)
}

func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	code := r.PostFormValue("code")
	owner, err := s.services.Friends.RedeemInvite(r.Context(), viewer, code,
		r.PostFormValue("note"), s.clientKey(r))
	if err != nil {
		p := s.newPage(r, "Use a request code", "friends", redeemPage{
			Code:  code,
			Error: userMessage(err, http.StatusBadRequest),
		})
		s.render(w, r, http.StatusBadRequest, "redeem.html", p)
		return
	}
	s.flashGood(w,
		"Your request is with "+owner.DisplayName+".",
		"Codes are one use only, so that one is spent now. They still need to accept, because a code can be forwarded on and the last word about who is in their life should be theirs.",
	)
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleAcceptRequest(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Accept(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "You are friends. You can see each other's posts from now on.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleDeclineRequest(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Decline(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "Turned down. They are not told, and they cannot see anything of yours.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleCancelRequest(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Cancel(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "Request withdrawn.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleUnfriend(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Unfriend(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "You are no longer friends. Neither of you can see the other's posts.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Block(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w,
			"Blocked.",
			"They cannot reach you by email address or by code, any friendship between you has ended, and anything either of you had waiting has been withdrawn.",
		)
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	viewer := viewerFrom(r.Context())
	if err := s.services.Friends.Unblock(r.Context(), viewer, domain.ID(r.PathValue("id"))); err != nil {
		s.flashBad(w, userMessage(err, http.StatusNotFound))
	} else {
		s.flashGood(w, "Unblocked. You are not friends again; that would need one of you to ask.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}
