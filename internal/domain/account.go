package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Role determines which capabilities an account holds. Amici deliberately keeps
// the set small: there is no "page", "brand" or "advertiser" role, and there
// never will be.
type Role string

const (
	// RoleMember is a human being using Amici to keep up with the people they
	// love. The overwhelming majority of accounts are members.
	RoleMember Role = "member"

	// RoleSupport helps members recover accounts and handles reports. Support
	// can resolve an account from an email address and suspend it, but can
	// never read a member's posts, comments or friend list.
	RoleSupport Role = "support"

	// RoleDeveloper operates the service: schema state, feature flags,
	// diagnostics. Developers get no window into member content either.
	RoleDeveloper Role = "developer"
)

// ParseRole validates untrusted input against the known roles.
func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleMember:
		return RoleMember, nil
	case RoleSupport:
		return RoleSupport, nil
	case RoleDeveloper:
		return RoleDeveloper, nil
	default:
		return "", fmt.Errorf("%w: unknown role %q", ErrValidation, s)
	}
}

// Capability is a single permission checked at the service boundary. Handlers
// ask "may this actor do this?" rather than "is this actor an admin?", so new
// roles can be introduced without auditing every call site.
type Capability string

const (
	// CapPostContent covers writing posts, comments and reactions.
	CapPostContent Capability = "content.write"
	// CapEditOwnCanvas covers customising your own profile HTML.
	CapEditOwnCanvas Capability = "canvas.write"
	// CapSendFriendRequests covers reaching out via email or invite code.
	CapSendFriendRequests Capability = "friends.request"

	// CapLookupAccountByEmail lets support resolve an account for recovery.
	// It returns account metadata only, never content.
	CapLookupAccountByEmail Capability = "support.lookup"
	// CapSuspendAccount lets support stop an account from signing in.
	CapSuspendAccount Capability = "support.suspend"
	// CapReviewReports lets support triage member reports.
	CapReviewReports Capability = "support.reports"
	// CapDisableCanvas lets support switch a profile back to plain rendering
	// when a canvas is used to harass or deceive.
	CapDisableCanvas Capability = "support.canvas.disable"

	// CapViewDiagnostics exposes build, schema and runtime health.
	CapViewDiagnostics Capability = "developer.diagnostics"
	// CapManageFeatureFlags exposes the flag surface.
	CapManageFeatureFlags Capability = "developer.flags"
)

var roleCapabilities = map[Role]map[Capability]bool{
	RoleMember: {
		CapPostContent:        true,
		CapEditOwnCanvas:      true,
		CapSendFriendRequests: true,
	},
	RoleSupport: {
		CapPostContent:          true,
		CapEditOwnCanvas:        true,
		CapSendFriendRequests:   true,
		CapLookupAccountByEmail: true,
		CapSuspendAccount:       true,
		CapReviewReports:        true,
		CapDisableCanvas:        true,
	},
	RoleDeveloper: {
		CapPostContent:        true,
		CapEditOwnCanvas:      true,
		CapSendFriendRequests: true,
		CapViewDiagnostics:    true,
		CapManageFeatureFlags: true,
	},
}

// Can reports whether the role holds the capability.
func (r Role) Can(c Capability) bool { return roleCapabilities[r][c] }

// Label is the human name shown in the interface.
func (r Role) Label() string {
	switch r {
	case RoleMember:
		return "Member"
	case RoleSupport:
		return "Support"
	case RoleDeveloper:
		return "Developer"
	default:
		return string(r)
	}
}

// Status is the lifecycle state of an account.
type Status string

const (
	// StatusActive accounts can sign in and use Amici normally.
	StatusActive Status = "active"
	// StatusSuspended accounts cannot sign in. Their content stays hidden from
	// feeds until the suspension is lifted.
	StatusSuspended Status = "suspended"
	// StatusDeactivated accounts were closed by their owner.
	StatusDeactivated Status = "deactivated"
)

// MinimumAgeYears is the youngest we will register. Amici is not a safe place
// to be a nine year old on the open internet, so we do not pretend otherwise.
const MinimumAgeYears = 13

// AdultAgeYears is the threshold above which the extra protections applied to
// young members are lifted.
const AdultAgeYears = 18

// Account is a person on Amici.
type Account struct {
	ID           ID
	Handle       string
	DisplayName  string
	Email        string
	EmailNorm    string
	PasswordHash string
	Role         Role
	Status       Status
	BirthDate    Date
	Colourway    string
	Bio          string
	// ReachableByEmail lets a member opt out of email-addressed friend
	// requests entirely, leaving invite codes as the only way in. It is
	// forced off for young members.
	ReachableByEmail bool
	CanvasDisabled   bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AgeAt returns the account holder's age in whole years at the given instant.
func (a *Account) AgeAt(t time.Time) int { return a.BirthDate.AgeAt(t) }

// IsYoungMember reports whether extra child-protection rules apply. Young
// members cannot be reached by anyone who merely knows their email address:
// an invite code they chose to share is the only route to them.
func (a *Account) IsYoungMember(now time.Time) bool { return a.AgeAt(now) < AdultAgeYears }

// AcceptsEmailRequests reports whether a friend request addressed to this
// account's email address may be delivered.
func (a *Account) AcceptsEmailRequests(now time.Time) bool {
	if a.Status != StatusActive {
		return false
	}
	if a.IsYoungMember(now) {
		return false
	}
	return a.ReachableByEmail
}

// InviteTTL is how long a friend request identifier stays usable. The brief is
// explicit: twenty four hours, no renewals, no "shareable profile links".
const InviteTTL = 24 * time.Hour

// YoungMemberInviteTTL shortens the window further for young members.
const YoungMemberInviteTTL = 4 * time.Hour

// InviteTTLFor returns the lifetime of an invite minted by this account.
func (a *Account) InviteTTLFor(now time.Time) time.Duration {
	if a.IsYoungMember(now) {
		return YoungMemberInviteTTL
	}
	return InviteTTL
}

const (
	handleMinLen      = 3
	handleMaxLen      = 24
	displayNameMaxLen = 60
	bioMaxLen         = 280
	emailMaxLen       = 254
)

var handlePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_.-]*[a-z0-9])?$`)

// reservedHandles are kept back so nobody can impersonate the service or
// shadow a routing prefix.
var reservedHandles = map[string]bool{
	"admin": true, "support": true, "help": true, "amici": true, "root": true,
	"api": true, "static": true, "media": true, "settings": true, "signin": true,
	"signup": true, "signout": true, "feed": true, "friends": true, "u": true,
	"developer": true, "dev": true, "system": true, "security": true, "abuse": true,
	"official": true, "moderator": true, "mod": true, "billing": true, "ads": true,
}

// NormaliseHandle lowercases and trims a handle for comparison and storage.
func NormaliseHandle(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidateHandle checks a handle for shape and availability against reserved
// names. Uniqueness is enforced by the store.
func ValidateHandle(s string) (string, error) {
	h := NormaliseHandle(s)
	if len(h) < handleMinLen || len(h) > handleMaxLen {
		return "", fmt.Errorf("%w: your handle needs to be between %d and %d characters", ErrValidation, handleMinLen, handleMaxLen)
	}
	if !handlePattern.MatchString(h) {
		return "", fmt.Errorf("%w: handles can use lowercase letters, numbers, dots, dashes and underscores, and must start and end with a letter or number", ErrValidation)
	}
	if strings.Contains(h, "..") {
		return "", fmt.Errorf("%w: handles cannot contain two dots in a row", ErrValidation)
	}
	if reservedHandles[h] {
		return "", fmt.Errorf("%w: that handle is reserved", ErrValidation)
	}
	return h, nil
}

// NormaliseEmail lowercases and trims an address so that lookups are stable.
// We deliberately do not strip dots or plus-addressing: an address is whatever
// the member typed, and treating variants as one identity would let someone
// probe for accounts they were not given.
func NormaliseEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidateEmail applies conservative structural checks. Deliverability is
// proven by the confirmation flow, not by a regex.
func ValidateEmail(s string) (string, error) {
	e := NormaliseEmail(s)
	if e == "" {
		return "", fmt.Errorf("%w: we need an email address", ErrValidation)
	}
	if len(e) > emailMaxLen {
		return "", fmt.Errorf("%w: that email address is too long", ErrValidation)
	}
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 {
		return "", fmt.Errorf("%w: that does not look like an email address", ErrValidation)
	}
	local, host := e[:at], e[at+1:]
	if strings.ContainsAny(e, " \t\r\n") {
		return "", fmt.Errorf("%w: that does not look like an email address", ErrValidation)
	}
	if local == "" || !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", fmt.Errorf("%w: that does not look like an email address", ErrValidation)
	}
	return e, nil
}

// ValidateDisplayName keeps names printable and a sensible length. Members may
// call themselves whatever they like within that.
func ValidateDisplayName(s string) (string, error) {
	n := strings.TrimSpace(s)
	if n == "" {
		return "", fmt.Errorf("%w: we need a name to show your friends", ErrValidation)
	}
	if len([]rune(n)) > displayNameMaxLen {
		return "", fmt.Errorf("%w: names can be at most %d characters", ErrValidation, displayNameMaxLen)
	}
	for _, r := range n {
		if r != '\t' && unicode.IsControl(r) {
			return "", fmt.Errorf("%w: names cannot contain control characters", ErrValidation)
		}
	}
	return n, nil
}

// ValidateBio trims and length-checks the short plain-text introduction shown
// above a profile canvas.
func ValidateBio(s string) (string, error) {
	b := strings.TrimSpace(s)
	if len([]rune(b)) > bioMaxLen {
		return "", fmt.Errorf("%w: introductions can be at most %d characters", ErrValidation, bioMaxLen)
	}
	return b, nil
}
