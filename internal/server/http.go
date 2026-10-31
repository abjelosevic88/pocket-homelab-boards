package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	r.Get("/pocket-ask.js", s.core.AskWidget("boards"))
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	s.mountDeck(r)

	r.Route("/api/v1", func(r chi.Router) {
		// Calendar apps can't log in: the feed's address carries its own key.
		r.Get("/calendar.ics", s.handleICS)

		// Live updates outlive the API timeout.
		r.With(s.requireAuth).Get("/boards/{id}/events", s.handleBoardEvents)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(120 * time.Second))
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/setup", s.handleSetup)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Get("/auth/oidc/start", s.handleOIDCStart)
			r.Get("/auth/oidc/callback", s.handleOIDCCallback)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(120 * time.Second))
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Put("/auth/me", s.handleUpdateMe)

			// overview
			r.Get("/home", s.handleHome)
			r.Get("/cards", s.handleCards)
			r.Post("/cards", s.handleCreateCardAnywhere)
			r.Get("/search", s.handleSearch)
			r.Get("/people", s.handlePeople)
			r.Get("/templates", s.handleTemplates)
			r.Get("/trash", s.handleTrash)
			r.Get("/overlay", s.handleOverlay)
			r.Get("/day/{date}", s.handleDay)

			// boards
			r.Get("/boards", s.handleListBoards)
			r.Post("/boards", s.handleCreateBoard)
			r.Get("/boards/{id}", s.handleGetBoard)
			r.Put("/boards/{id}", s.handleUpdateBoard)
			r.Delete("/boards/{id}", s.handleDeleteBoard)
			r.Post("/boards/{id}/restore", s.handleRestoreBoard)
			r.Post("/boards/{id}/copy", s.handleCopyBoard)
			r.Get("/boards/{id}/activity", s.handleBoardActivity)
			r.Get("/boards/{id}/members", s.handleMembers)
			r.Post("/boards/{id}/members", s.handleAddMember)
			r.Put("/boards/{id}/members/{uid}", s.handleUpdateMember)
			r.Delete("/boards/{id}/members/{uid}", s.handleRemoveMember)
			r.Post("/boards/{id}/lists", s.handleCreateList)
			r.Post("/boards/{id}/labels", s.handleCreateLabel)

			// lists and labels
			r.Put("/lists/{id}", s.handleUpdateList)
			r.Delete("/lists/{id}", s.handleDeleteList)
			r.Post("/lists/{id}/restore", s.handleRestoreList)
			r.Post("/lists/{id}/archive-cards", s.handleArchiveListCards)
			r.Post("/lists/{id}/sort", s.handleSortList)
			r.Post("/lists/{id}/cards", s.handleCreateCard)
			r.Put("/labels/{id}", s.handleUpdateLabel)
			r.Delete("/labels/{id}", s.handleDeleteLabel)

			// cards
			r.Get("/cards/{id}", s.handleGetCard)
			r.Put("/cards/{id}", s.handleUpdateCard)
			r.Delete("/cards/{id}", s.handleDeleteCard)
			r.Post("/cards/{id}/restore", s.handleRestoreCard)
			r.Post("/cards/{id}/copy", s.handleCopyCard)
			r.Put("/cards/{id}/labels/{label}", s.handleCardLabel)
			r.Delete("/cards/{id}/labels/{label}", s.handleCardLabel)
			r.Put("/cards/{id}/members/{uid}", s.handleCardMember)
			r.Delete("/cards/{id}/members/{uid}", s.handleCardMember)
			r.Put("/cards/{id}/checklist/{index}", s.handleChecklist)
			r.Get("/cards/{id}/comments", s.handleListComments)
			r.Post("/cards/{id}/comments", s.handleCreateComment)
			r.Put("/comments/{id}", s.handleUpdateComment)
			r.Delete("/comments/{id}", s.handleDeleteComment)
			r.Get("/cards/{id}/attachments", s.handleListAttachments)
			r.Post("/cards/{id}/attachments", s.handleUploadAttachment)
			r.Get("/attachments/{id}", s.handleGetAttachment)
			r.Delete("/attachments/{id}", s.handleDeleteAttachment)

			// Pocket Tasks and Pocket Time
			r.Post("/cards/{id}/task", s.handleCardToTask)
			r.Post("/cards/{id}/timer", s.handleCardTimer)
			r.Get("/timer", s.handleTimer)
			r.Get("/worklog", s.handleWorklogList)
			r.Get("/worklog/items", s.handleWorklogItems)
			r.Post("/worklog", s.handleWorklogSave)
			r.Delete("/worklog/{id}", s.handleWorklogDelete)

			// AI
			r.Get("/ai/status", s.handleAIStatus)
			r.Get("/ai/models", s.handleAIModels)
			r.Post("/ai/checklist", s.handleAIChecklist)
			r.Post("/ai/board", s.handleAIBoard)

			// import from Nextcloud Deck and Trello; Deck clients' sign-in
			r.Get("/import/deck/boards", s.handleDeckImportBoards)
			r.Post("/import/deck", s.handleImportDeck)
			r.Post("/import/trello", s.handleImportTrello)
			r.Get("/loginflow/{token}", s.handleLoginFlowInfo)
			r.Post("/loginflow/{token}/approve", s.handleLoginFlowApprove)

			r.Post("/integrations/test", s.handleIntegrationTest)
			r.Post("/notify/test", s.handleNotifyTest)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Get("/settings/system", s.handleSystemInfo)
			r.Post("/settings/ics-token", s.handleNewICSToken)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/backup", s.handleBackup)
			r.Get("/export.json", s.handleExportJSON)
			r.Get("/export.csv", s.handleExportCSV)
			r.Post("/import", s.handleImportJSON)
		})
	})

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, core.ErrOperationConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func (s *Server) readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, s.cfg.MaxBodyBytes))
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// decodeKeys decodes JSON into v and also reports which top-level keys were
// present, so PUT handlers can tell "not sent" from "sent empty".
func decodeKeys(r *http.Request, v any) (map[string]bool, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys, json.Unmarshal(b, v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// dateRange reads ?from&to (defaults: the last n days up to today).
func dateRange(r *http.Request, days int) (string, string) {
	to := r.URL.Query().Get("to")
	from := r.URL.Query().Get("from")
	if to == "" || !validDate(to) {
		to = store.Today()
	}
	if from == "" || !validDate(from) {
		from = store.AddDays(to, -(days - 1))
	}
	if from > to {
		from, to = to, from
	}
	return from, to
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"core_outbox": s.coreOutboxHealth(r.Context()), "status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_boards_info Build info.\n# TYPE pocket_boards_info gauge\npocket_boards_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_boards_uptime_seconds Process uptime.\n# TYPE pocket_boards_uptime_seconds gauge\npocket_boards_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_boards_http_requests_total HTTP requests served.\n# TYPE pocket_boards_http_requests_total counter\npocket_boards_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_boards_http_errors_total HTTP 5xx responses.\n# TYPE pocket_boards_http_errors_total counter\npocket_boards_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_boards_rows Rows per table.\n# TYPE pocket_boards_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_boards_rows{table=%q} %d\n", k, v)
	}

}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}
