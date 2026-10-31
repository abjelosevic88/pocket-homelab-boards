package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- Nextcloud Login Flow v2 ----
//
// A Deck client asks for a sign-in (POST /index.php/login/v2), opens the
// returned address in the browser, where the person signs in to Boards and
// allows it (the /login/flow/<token> page), and polls until it receives its
// own API token as the "app password". The token appears once and is listed
// in Settings → API & shortcuts like any other.

const loginFlowTTL = 20 * time.Minute

func (s *Server) mountLoginFlow(r chi.Router) {
	for _, p := range []string{"/index.php/login/v2", "/login/v2"} {
		r.Post(p, s.handleLoginFlowStart)
		r.Post(p+"/poll", s.handleLoginFlowPoll)
	}
}

// publicBase is BASE_URL, or what the request came in on.
func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return strings.TrimRight(s.cfg.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleLoginFlowStart(w http.ResponseWriter, r *http.Request) {
	client := strings.TrimSpace(r.UserAgent())
	if client == "" {
		client = "a Deck app"
	}
	client = excerpt(client, 80)
	poll, login := store.RandomToken(32), store.RandomToken(24)
	if err := s.store.CreateLoginFlow(r.Context(), poll, login, client, loginFlowTTL); err != nil {
		s.fail(w, err, "login flow")
		return
	}
	base := s.publicBase(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"poll":  map[string]any{"token": poll, "endpoint": base + "/index.php/login/v2/poll"},
		"login": base + "/login/flow/" + login,
	})
}

func (s *Server) handleLoginFlowPoll(w http.ResponseWriter, r *http.Request) {
	tok := r.FormValue("token")
	if tok == "" {
		var in struct {
			Token string `json:"token"`
		}
		_ = decode(r, &in)
		tok = in.Token
	}
	if tok == "" {
		writeJSON(w, http.StatusNotFound, []any{})
		return
	}
	f, err := s.store.PollLoginFlow(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusNotFound, []any{})
		return
	}
	u, err := s.store.GetUser(r.Context(), *f.UserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, []any{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": s.publicBase(r), "loginName": u.Email, "appPassword": f.AppPassword})
}

// handleLoginFlowInfo is what the approval page shows: which app asks.
func (s *Server) handleLoginFlowInfo(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.LoginFlowByLogin(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this sign-in link has expired; start again in the app")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client_name": f.ClientName, "expires_at": f.ExpiresAt, "approved": f.UserID != nil})
}

// handleLoginFlowApprove makes the app its own API token for the signed-in person.
func (s *Server) handleLoginFlowApprove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "sign in yourself to allow an app")
		return
	}
	login := chi.URLParam(r, "token")
	f, err := s.store.LoginFlowByLogin(ctx, login)
	if err != nil || f.UserID != nil {
		writeErr(w, http.StatusNotFound, "this sign-in link has expired or was already used; start again in the app")
		return
	}
	uid := actorID(ctx)
	plain, tok, err := s.store.CreateAPIToken(ctx, uid, excerpt("Deck: "+f.ClientName, 100))
	if err != nil {
		s.fail(w, err, "login flow")
		return
	}
	if err := s.store.ApproveLoginFlow(ctx, login, uid, plain); err != nil {
		_ = s.store.DeleteAPIToken(ctx, uid, tok.ID)
		writeErr(w, http.StatusNotFound, "this sign-in link has expired or was already used; start again in the app")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "client_name": f.ClientName})
}
