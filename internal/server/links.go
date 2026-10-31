package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Boards as
// the target: another Pocket app the person allowed in Core may act here
// for them with a pcx_ token, only within these scopes. Every route not
// listed in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "boards:read", Name: "See your boards", Description: "Read your boards, lists and cards with their due dates — for your journal, calendar and dashboards. Comments and files stay here."},
	{Scope: "boards:write", Name: "Add and move cards", Description: "Add cards to your boards, change them, tick them done and comment (\"make this a card\" in another app). It can't delete boards or share them."},
	{Scope: "worklog:read", Name: "See time spent on cards", Description: "Read the time logged on your cards (what Pocket Time shows)."},
	{Scope: "worklog:write", Name: "Log time on cards", Description: "Record time tracked in Pocket Time on a card. It may change only the time it logged itself."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(home|boards|cards|search|overlay)$`), "boards:read"},
	{"GET", regexp.MustCompile(`^/api/v1/boards/\d+$`), "boards:read"},
	{"GET", regexp.MustCompile(`^/api/v1/cards/\d+$`), "boards:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "boards:read"},
	{"POST", regexp.MustCompile(`^/api/v1/lists/\d+/cards$`), "boards:write"},
	{"POST", regexp.MustCompile(`^/api/v1/cards$`), "boards:write"},
	{"PUT", regexp.MustCompile(`^/api/v1/cards/\d+$`), "boards:write"},
	{"PUT", regexp.MustCompile(`^/api/v1/cards/\d+/checklist/\d+$`), "boards:write"},
	{"POST", regexp.MustCompile(`^/api/v1/cards/\d+/comments$`), "boards:write"},
	{"GET", regexp.MustCompile(`^/api/v1/worklog(/items)?$`), "worklog:read"},
	{"POST", regexp.MustCompile(`^/api/v1/worklog$`), "worklog:write"},
	{"DELETE", regexp.MustCompile(`^/api/v1/worklog/[^/]+$`), "worklog:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
