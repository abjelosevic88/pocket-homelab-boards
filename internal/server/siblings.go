package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// ---- Pocket Tasks and Pocket Time ----
//
// Each person chooses how Boards reaches them: through a Pocket Core app
// link (Boards asks Core for a five-minute token acting as that person,
// nothing to paste), directly with their own URL + API token, or not at all.
// Core tokens are asked for per request with only the scopes the work needs
// and never kept, so a link switched off in Core applies at once.

const (
	scopeTasksWrite = "tasks:write"
	scopeTimeRead   = "time:read"
	scopeTimeWrite  = "time:write"
)

// pocketApp is one sibling Pocket app Boards acts in.
type pocketApp struct {
	slug, name string
	scopes     []string
	reason     string
}

var (
	appTasks   = pocketApp{"tasks", "Pocket Tasks", []string{scopeTasksWrite}, "Send a card to your task list, with its due date and a link back"}
	appTime    = pocketApp{"time", "Pocket Time", []string{scopeTimeRead, scopeTimeWrite}, "Start your timer on a card; the time lands back on the card"}
	pocketApps = []pocketApp{appTasks, appTime}
)

func appBySlug(slug string) (pocketApp, bool) {
	for _, a := range pocketApps {
		if a.slug == slug {
			return a, true
		}
	}
	return pocketApp{}, false
}

// linkUses is the manifest's Uses.
func linkUses() []core.Use {
	out := make([]core.Use, 0, len(pocketApps))
	for _, a := range pocketApps {
		out = append(out, core.Use{App: a.slug, Scopes: a.scopes, Reason: a.reason})
	}
	return out
}

// Connection modes, chosen per person and per app in Settings.
const (
	modeCore   = "core"
	modeDirect = "direct"
	modeOff    = "off"
)

// connMode is the mode in effect: the chosen one, else direct when a token
// was entered, else Pocket Core when Boards is connected to one, else direct.
func (s *Server) connMode(mode, token string) string {
	switch mode {
	case modeCore, modeDirect, modeOff:
		return mode
	}
	if token != "" || !s.core.Configured() {
		return modeDirect
	}
	return modeCore
}

// notConnected means the person has no working connection to the app.
type notConnected struct{ msg string }

func (e *notConnected) Error() string { return e.msg }

func isNotConnected(err error) bool {
	var nc *notConnected
	return errors.As(err, &nc)
}

// siblingConn is how Boards reaches one person's copy of a sibling app.
type siblingConn struct {
	client *pocket.Client
	via    string
	public string
}

func linkReason(app, reason string) string {
	switch reason {
	case "off_person":
		return "you switched the Boards → " + app + " link off in Pocket Core (Settings → Apps acting for me)"
	case "off_admin":
		return "the Boards → " + app + " link is switched off in Pocket Core (Links)"
	case "target_missing":
		return app + " is not registered with Pocket Core"
	case "not_offered":
		return "this " + app + " version does not accept app links yet"
	case "not_declared":
		return "Pocket Core has not seen this Boards version yet; it re-registers within a minute of starting"
	case "not_in_audience":
		return "you do not have access to " + app + " in Pocket Core"
	case "unknown_user":
		return "your email is not a person in Pocket Core"
	}
	return ""
}

// connSettings returns a person's saved mode, URL, token and public URL for an app.
func connSettings(st store.Settings, a pocketApp) (mode, url, token, public string) {
	switch a.slug {
	case "tasks":
		return st.TasksMode, st.TasksURL, st.TasksToken, st.TasksPublicURL
	case "time":
		return st.TimeMode, st.TimeURL, st.TimeToken, st.TimePublicURL
	}
	return "", "", "", ""
}

// siblingFor connects to app a for person u with exactly these scopes.
func (s *Server) siblingFor(ctx context.Context, u *store.User, st store.Settings, a pocketApp, scopes ...string) (*siblingConn, error) {
	if u == nil {
		return nil, &notConnected{"nobody to act for"}
	}
	mode, url, token, public := connSettings(st, a)
	switch s.connMode(mode, token) {
	case modeOff:
		return nil, &notConnected{a.name + " is switched off in Settings → Connected apps"}
	case modeDirect:
		if token == "" {
			return nil, &notConnected{a.name + " is not set up (Settings → Connected apps): add a URL and token, or switch to Pocket Core"}
		}
		base := url
		if base == "" {
			base = s.resolveSibling(ctx, a.slug)
		}
		if base == "" {
			return nil, &notConnected{"set the " + a.name + " URL in Settings → Connected apps"}
		}
		return &siblingConn{client: pocket.New(base, token, 15*time.Second), via: modeDirect, public: firstNonEmpty(public, base)}, nil
	}
	if !s.core.Configured() {
		return nil, &notConnected{a.name + " through Pocket Core: there is no Pocket Core on this box; use a direct connection"}
	}
	tok, err := s.core.ActAs(ctx, u.Email, a.slug, scopes...)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			if t := linkReason(a.name, le.Reason); t != "" {
				return nil, &notConnected{a.name + " is not set up: " + t}
			}
			return nil, &notConnected{a.name + " is not set up: " + le.Error()}
		}
		return nil, fmt.Errorf("%s through Pocket Core: %w", a.name, err)
	}
	if tok.URL == "" || tok.AccessToken == "" {
		return nil, fmt.Errorf("%s through Pocket Core: Core gave no address or token", a.name)
	}
	return &siblingConn{client: pocket.New(tok.URL, tok.AccessToken, 15*time.Second), via: modeCore, public: firstNonEmpty(public, firstNonEmpty(tok.PublicURL, tok.URL))}, nil
}

// connStatus says, without issuing anything, how a person reaches an app.
func (s *Server) connStatus(ctx context.Context, u *store.User, st store.Settings, a pocketApp) (eff, via, detail string) {
	mode, _, token, _ := connSettings(st, a)
	eff = s.connMode(mode, token)
	switch eff {
	case modeOff:
		return eff, "", "switched off"
	case modeDirect:
		if token == "" {
			return eff, "", "no token yet"
		}
		return eff, modeDirect, ""
	}
	if u == nil || !s.core.Configured() {
		return eff, "", "there is no Pocket Core on this box"
	}
	list, err := s.core.LinkStatus(ctx, u.Email, a.slug)
	if err != nil {
		return eff, "", "Pocket Core did not answer: " + err.Error()
	}
	state := map[string]core.LinkState{}
	for _, l := range list {
		state[l.Scope] = l
	}
	var on, off []string
	reason := ""
	for _, sc := range a.scopes {
		l, ok := state[sc]
		if ok && l.On {
			on = append(on, sc)
			continue
		}
		off = append(off, sc)
		if reason == "" {
			reason = "not_declared"
			if ok {
				reason = l.State
			}
		}
	}
	text := linkReason(a.name, reason)
	if text == "" {
		text = reason
	}
	switch {
	case len(off) == 0:
		return eff, modeCore, ""
	case len(on) == 0:
		return eff, "", text
	}
	sort.Strings(off)
	return eff, modeCore, "partly: " + strings.Join(off, ", ") + " off (" + text + ")"
}

// clients carries one person's settings and their connections for one request.
type clients struct {
	s      *Server
	u      *store.User
	st     store.Settings
	notify *notify.Client
	conns  map[string]connResult
}

type connResult struct {
	c   *siblingConn
	err error
}

// clients builds the connections from ONE user's own settings.
func (s *Server) clients(ctx context.Context, uid int64) *clients {
	st, _ := s.store.GetSettings(ctx, uid)
	u := userFrom(ctx)
	if u == nil || u.ID != uid {
		u, _ = s.store.GetUser(ctx, uid)
	}
	return &clients{s: s, u: u, st: st, notify: notify.New(st.NotifyURL, st.NotifyFormat), conns: map[string]connResult{}}
}

func (c *clients) conn(ctx context.Context, a pocketApp, scopes ...string) (*siblingConn, error) {
	sc := append([]string(nil), scopes...)
	sort.Strings(sc)
	key := a.slug + " " + strings.Join(sc, " ")
	if r, ok := c.conns[key]; ok {
		return r.c, r.err
	}
	sib, err := c.s.siblingFor(ctx, c.u, c.st, a, scopes...)
	c.conns[key] = connResult{sib, err}
	return sib, err
}

// optional turns "not connected" into (nil, nil).
func optional(sib *siblingConn, err error) (*siblingConn, error) {
	if isNotConnected(err) {
		return nil, nil
	}
	return sib, err
}
