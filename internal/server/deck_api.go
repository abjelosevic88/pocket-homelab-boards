package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// ---- Nextcloud Deck–compatible API ----
//
// Deck clients (and scripts written for Deck) talk to Boards as if it were a
// Nextcloud with the Deck app: /index.php/apps/deck/api/v1.0|v1.1/…, the OCS
// comments API, /status.php and the capabilities. Deck stacks are our lists,
// "order" is the position, users are identified by their email as uid.
// Every change goes through the same code as the Boards API (done lists,
// repeating cards, activity, live updates, events), marked as coming from Deck.

// mountDeck adds the Nextcloud-compatible routes (Deck API, OCS, status.php, login flow).
func (s *Server) mountDeck(r chi.Router) {
	r.Get("/status.php", s.handleStatusPHP)
	r.Get("/index.php/status.php", s.handleStatusPHP)

	for _, p := range []string{"/index.php/apps/deck/api/{v:v1\\.[01]}", "/apps/deck/api/{v:v1\\.[01]}"} {
		r.Route(p, func(r chi.Router) {
			r.Use(s.deckAuth(false))
			r.Get("/boards", s.deckListBoards)
			r.Post("/boards", s.deckCreateBoard)
			r.Get("/boards/{boardId}", s.deckGetBoard)
			r.Put("/boards/{boardId}", s.deckUpdateBoard)
			r.Delete("/boards/{boardId}", s.deckDeleteBoard)
			r.Post("/boards/{boardId}/undo_delete", s.deckUndoDeleteBoard)
			r.Get("/boards/{boardId}/acl", s.deckListACL)
			r.Post("/boards/{boardId}/acl", s.deckAddACL)
			r.Put("/boards/{boardId}/acl/{aclId}", s.deckUpdateACL)
			r.Delete("/boards/{boardId}/acl/{aclId}", s.deckDeleteACL)

			r.Get("/boards/{boardId}/labels", s.deckListLabels)
			r.Post("/boards/{boardId}/labels", s.deckCreateLabel)
			r.Get("/boards/{boardId}/labels/{labelId}", s.deckGetLabel)
			r.Put("/boards/{boardId}/labels/{labelId}", s.deckUpdateLabel)
			r.Delete("/boards/{boardId}/labels/{labelId}", s.deckDeleteLabel)

			r.Get("/boards/{boardId}/stacks", s.deckListStacks)
			r.Get("/boards/{boardId}/stacks/archived", s.deckArchivedStacks)
			r.Post("/boards/{boardId}/stacks", s.deckCreateStack)
			r.Get("/boards/{boardId}/stacks/{stackId}", s.deckGetStack)
			r.Put("/boards/{boardId}/stacks/{stackId}", s.deckUpdateStack)
			r.Delete("/boards/{boardId}/stacks/{stackId}", s.deckDeleteStack)

			c := "/boards/{boardId}/stacks/{stackId}/cards"
			r.Get(c, s.deckListCards)
			r.Post(c, s.deckCreateCard)
			r.Get(c+"/{cardId}", s.deckGetCard)
			r.Put(c+"/{cardId}", s.deckUpdateCard)
			r.Delete(c+"/{cardId}", s.deckDeleteCard)
			r.Put(c+"/{cardId}/assignLabel", s.deckCardLabel(true))
			r.Put(c+"/{cardId}/removeLabel", s.deckCardLabel(false))
			r.Put(c+"/{cardId}/assignUser", s.deckCardUser(true))
			r.Put(c+"/{cardId}/unassignUser", s.deckCardUser(false))
			r.Put(c+"/{cardId}/reorder", s.deckReorderCard)
			r.Put(c+"/{cardId}/archive", s.deckCardFlag("archived", true))
			r.Put(c+"/{cardId}/unarchive", s.deckCardFlag("archived", false))
			r.Put(c+"/{cardId}/done", s.deckCardFlag("done", true))
			r.Put(c+"/{cardId}/undone", s.deckCardFlag("done", false))

			a := c + "/{cardId}/attachments"
			r.Get(a, s.deckListAttachments)
			r.Post(a, s.deckUploadAttachment)
			r.Get(a+"/{attachmentId}", s.deckGetAttachment)
			r.Get(a+"/{type}/{attachmentId}", s.deckGetAttachment)
			r.Delete(a+"/{attachmentId}", s.deckDeleteAttachment)
			r.Delete(a+"/{type}/{attachmentId}", s.deckDeleteAttachment)
			r.Put(a+"/{attachmentId}/restore", s.deckRestoreAttachment)
			r.Put(a+"/{type}/{attachmentId}/restore", s.deckRestoreAttachment)
		})
	}

	for _, v := range []string{"/ocs/v2.php", "/ocs/v1.php"} {
		r.Route(v, func(r chi.Router) {
			r.Get("/cloud/capabilities", s.ocsCapabilities)
			r.Group(func(r chi.Router) {
				r.Use(s.deckAuth(true))
				r.Get("/cloud/user", s.ocsUser)
				r.Get("/apps/deck/api/v1.0/config", s.ocsDeckConfig)
				r.Get("/apps/deck/api/v1.0/cards/{cardId}/comments", s.ocsListComments)
				r.Post("/apps/deck/api/v1.0/cards/{cardId}/comments", s.ocsCreateComment)
				r.Put("/apps/deck/api/v1.0/cards/{cardId}/comments/{commentId}", s.ocsUpdateComment)
				r.Delete("/apps/deck/api/v1.0/cards/{cardId}/comments/{commentId}", s.ocsDeleteComment)
			})
		})
	}

	s.mountLoginFlow(r)
	r.With(s.requireAuth).Get("/api/v1/import/deck/status", s.handleDeckImportStatus)
}

// ---- auth ----

// deckLimiter slows down guessing: after 20 failed Basic sign-ins from one
// address in 10 minutes, that address waits until the window passes.
type deckLimiter struct {
	mu sync.Mutex
	m  map[string]*deckFails
}

type deckFails struct {
	n     int
	first time.Time
}

var deckLimit = &deckLimiter{m: map[string]*deckFails{}}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (l *deckLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.m[ip]
	if f == nil {
		return false
	}
	if time.Since(f.first) > 10*time.Minute {
		delete(l.m, ip)
		return false
	}
	return f.n >= 20
}

func (l *deckLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.m[ip]
	if f == nil || time.Since(f.first) > 10*time.Minute {
		f = &deckFails{first: time.Now()}
		l.m[ip] = f
	}
	f.n++
}

var errTooMany = errors.New("too many failed sign-ins, try again in a few minutes")

// basicUser checks HTTP Basic credentials: the password may be an API token
// (pbo_…), a Pocket Core token, or the account's own password.
func (s *Server) basicUser(ctx context.Context, user, pass string) (*store.User, error) {
	if pass == "" {
		return nil, http.ErrNoCookie
	}
	if s.core.Configured() && core.IsLinkToken(pass) {
		return nil, http.ErrNoCookie
	}
	if s.core.Configured() && core.IsCoreToken(pass) {
		return s.coreUser(ctx, pass)
	}
	if strings.HasPrefix(pass, "pbo_") {
		return s.store.GetUserByAPIToken(ctx, pass)
	}
	u, err := s.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(user)))
	if err != nil || u.PasswordHash == "" {
		return nil, http.ErrNoCookie
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(pass)) != nil {
		return nil, http.ErrNoCookie
	}
	return u, nil
}

func (s *Server) deckUser(r *http.Request) (*store.User, error) {
	if user, pass, ok := r.BasicAuth(); ok {
		ip := clientIP(r)
		if deckLimit.blocked(ip) {
			return nil, errTooMany
		}
		u, err := s.basicUser(r.Context(), user, pass)
		if err != nil || u == nil {
			deckLimit.fail(ip)
			return nil, http.ErrNoCookie
		}
		return u, nil
	}
	u, app, err := s.resolveAuth(r)
	if err != nil || u == nil || app != "" {
		return nil, http.ErrNoCookie
	}
	return u, nil
}

// deckAuth signs the request in and marks it as coming through the Deck API.
func (s *Server) deckAuth(ocs bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, err := s.deckUser(r)
			if err != nil {
				status, msg := http.StatusUnauthorized, "Current user is not logged in"
				if errors.Is(err, errTooMany) {
					status, msg = http.StatusTooManyRequests, err.Error()
				}
				if status == http.StatusUnauthorized {
					w.Header().Set("WWW-Authenticate", `Basic realm="Pocket Boards", charset="UTF-8"`)
				}
				if ocs {
					ocsErr(w, r, status, msg)
				} else {
					deckErr(w, status, msg)
				}
				return
			}
			ctx := context.WithValue(r.Context(), userKey, u)
			ctx = context.WithValue(ctx, deckKey, true)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---- responses ----

func deckErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"status": status, "message": msg})
}

func deckNotFound(w http.ResponseWriter) { deckErr(w, http.StatusNotFound, "Not found") }

func ocsVersion1(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/ocs/v1.php") }

func ocsOK(w http.ResponseWriter, r *http.Request, data any) {
	code := 200
	if ocsVersion1(r) {
		code = 100
	}
	writeJSON(w, http.StatusOK, map[string]any{"ocs": map[string]any{"meta": map[string]any{"status": "ok", "statuscode": code, "message": "OK"}, "data": data}})
}

func ocsErr(w http.ResponseWriter, r *http.Request, status int, msg string) {
	code := status
	if status == http.StatusUnauthorized {
		code = 997
	}
	httpStatus := status
	if ocsVersion1(r) {
		httpStatus = http.StatusOK
		if status == http.StatusUnauthorized {
			httpStatus = http.StatusUnauthorized
		}
	}
	writeJSON(w, httpStatus, map[string]any{"ocs": map[string]any{"meta": map[string]any{"status": "failure", "statuscode": code, "message": msg}, "data": []any{}}})
}

// deckTime is Deck's ISO 8601 (+00:00, not Z).
func deckTime(v string) any {
	t, ok := parseTime(v)
	if !ok {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05-07:00")
}

func unixOf(v string) int64 {
	t, ok := parseTime(v)
	if !ok {
		return 0
	}
	return t.Unix()
}

func etagOf(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:16])
}

// ifModifiedSince reads the header (zero time when absent or unreadable).
func ifModifiedSince(r *http.Request) time.Time {
	if h := r.Header.Get("If-Modified-Since"); h != "" {
		if t, err := http.ParseTime(h); err == nil {
			return t
		}
	}
	return time.Time{}
}

// notModified answers 304 when the client already has this ETag.
func notModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", `"`+etag+`"`)
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Trim(inm, `"`) == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// ---- request bodies (JSON or form) ----

type deckBody map[string]any

func readDeckBody(r *http.Request) deckBody {
	out := deckBody{}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		if strings.HasPrefix(ct, "multipart/") {
			_ = r.ParseMultipartForm(32 << 20)
		} else {
			_ = r.ParseForm()
		}
		for k, v := range r.Form {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
		return out
	}
	_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20)).Decode(&out)
	return out
}

func (b deckBody) has(k string) bool { _, ok := b[k]; return ok }

func (b deckBody) str(k string) string {
	switch v := b[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (b deckBody) num(k string) (int64, bool) {
	switch v := b[k].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func (b deckBody) boolean(k string) bool {
	switch v := b[k].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	}
	return false
}

// null reports whether the key was sent as JSON null or empty.
func (b deckBody) null(k string) bool {
	v, ok := b[k]
	if !ok {
		return false
	}
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok && (s == "" || s == "null") {
		return true
	}
	return false
}

// ---- JSON shapes ----

type deckPeople struct {
	s     *Server
	ctx   context.Context
	cache map[int64]*store.User
}

func (s *Server) deckPeople(ctx context.Context) *deckPeople {
	return &deckPeople{s: s, ctx: ctx, cache: map[int64]*store.User{}}
}

func (p *deckPeople) user(id int64) *store.User {
	if u, ok := p.cache[id]; ok {
		return u
	}
	u, err := p.s.store.GetUser(p.ctx, id)
	if err != nil {
		u = nil
	}
	p.cache[id] = u
	return u
}

func deckUserObj(email, name string) map[string]any {
	return map[string]any{"primaryKey": email, "uid": email, "displayname": firstNonEmpty(name, email), "type": 0}
}

func (p *deckPeople) obj(id int64) map[string]any {
	if u := p.user(id); u != nil {
		return deckUserObj(u.Email, u.Name)
	}
	return deckUserObj("deleted_users", "Deleted user")
}

func (p *deckPeople) uid(id *int64) string {
	if id == nil {
		return ""
	}
	if u := p.user(*id); u != nil {
		return u.Email
	}
	return ""
}

func deckPermissions(role string) map[string]bool {
	return map[string]bool{"PERMISSION_READ": true, "PERMISSION_EDIT": role == store.RoleOwner || role == store.RoleEditor,
		"PERMISSION_MANAGE": role == store.RoleOwner, "PERMISSION_SHARE": role == store.RoleOwner}
}

func deckLabelJSON(l store.Label, cardID *int64) map[string]any {
	var cid any
	if cardID != nil {
		cid = *cardID
	}
	return map[string]any{"title": l.Title, "color": l.Color, "boardId": l.BoardID, "cardId": cid, "id": l.ID, "lastModified": 0, "ETag": etagOf("label", strconv.FormatInt(l.ID, 10), l.Title, l.Color)}
}

func deckACLJSON(boardID int64, m store.Member) map[string]any {
	return map[string]any{"participant": deckUserObj(m.Email, m.Name), "type": 0, "boardId": boardID, "permissionEdit": m.Role != store.RoleViewer,
		"permissionShare": false, "permissionManage": false, "owner": false, "id": m.UserID}
}

func (s *Server) deckBoardJSON(ctx context.Context, p *deckPeople, b *store.Board) (map[string]any, error) {
	labels, err := s.store.Labels(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	members, err := s.store.Members(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	ls := make([]any, 0, len(labels))
	for _, l := range labels {
		ls = append(ls, deckLabelJSON(l, nil))
	}
	acl, users := []any{}, []any{}
	for _, m := range members {
		users = append(users, deckUserObj(m.Email, m.Name))
		if m.Role != store.RoleOwner {
			acl = append(acl, deckACLJSON(b.ID, m))
		}
	}
	shared := 0
	if b.Role != store.RoleOwner {
		shared = 1
	}
	return map[string]any{"title": b.Title, "owner": p.obj(b.OwnerID), "color": b.Color, "archived": b.Archived, "labels": ls, "acl": acl,
		"permissions": deckPermissions(b.Role), "users": users, "shared": shared, "deletedAt": unixOf(b.DeletedAt), "id": b.ID,
		"lastModified": unixOf(b.UpdatedAt), "settings": map[string]any{"notify-due": "off", "calendar": true}, "ETag": etagOf("board", strconv.FormatInt(b.ID, 10), b.UpdatedAt)}, nil
}

func (s *Server) deckCardJSON(p *deckPeople, c *store.Card, labels map[int64]store.Label, atts []store.Attachment) map[string]any {
	ls := []any{}
	for _, id := range c.Labels {
		if l, ok := labels[id]; ok {
			ls = append(ls, deckLabelJSON(l, &c.ID))
		}
	}
	assigned := []any{}
	for _, m := range c.Members {
		assigned = append(assigned, map[string]any{"id": m, "participant": p.obj(m), "cardId": c.ID, "type": 0})
	}
	var attJSON any
	if atts != nil {
		list := []any{}
		for _, a := range atts {
			list = append(list, s.deckAttachmentJSON(p, &a))
		}
		attJSON = list
	}
	var owner any = ""
	if c.CreatedBy != nil {
		owner = p.obj(*c.CreatedBy)
	}
	overdue := 0
	if t, ok := parseTime(c.DueAt); ok && c.DoneAt == "" {
		now := time.Now()
		switch {
		case t.Before(now):
			overdue = 3
		case t.Before(endOfDay(now)):
			overdue = 2
		case t.Before(endOfDay(now).AddDate(0, 0, 1)):
			overdue = 1
		}
	}
	desc := c.Description
	return map[string]any{"title": c.Title, "description": desc, "stackId": c.ListID, "type": "plain", "lastModified": unixOf(c.UpdatedAt), "lastEditor": p.uid(c.UpdatedBy),
		"createdAt": unixOf(c.CreatedAt), "labels": ls, "assignedUsers": assigned, "attachments": attJSON, "attachmentCount": c.Attachments, "owner": owner,
		"order": c.Position, "archived": c.Archived, "done": deckTime(c.DoneAt), "duedate": deckTime(c.DueAt), "deletedAt": unixOf(c.DeletedAt),
		"commentsUnread": 0, "commentsCount": c.Comments, "id": c.ID, "overdue": overdue, "referenceData": nil,
		"ETag": etagOf("card", strconv.FormatInt(c.ID, 10), c.UpdatedAt, strconv.Itoa(c.Comments), strconv.Itoa(c.Attachments))}
}

func (s *Server) deckAttachmentJSON(p *deckPeople, a *store.Attachment) map[string]any {
	ext := strings.TrimPrefix(filepath.Ext(a.Filename), ".")
	base := strings.TrimSuffix(a.Filename, filepath.Ext(a.Filename))
	return map[string]any{"cardId": a.CardID, "type": "deck_file", "data": a.Filename, "lastModified": unixOf(a.CreatedAt), "createdAt": unixOf(a.CreatedAt),
		"createdBy": p.uid(a.UserID), "deletedAt": unixOf(a.DeletedAt), "id": a.ID,
		"extendedData": map[string]any{"filesize": a.Size, "mimetype": a.Mime, "info": map[string]any{"dirname": ".", "basename": a.Filename, "extension": ext, "filename": base}}}
}

func labelMap(ls []store.Label) map[int64]store.Label {
	m := make(map[int64]store.Label, len(ls))
	for _, l := range ls {
		m[l.ID] = l
	}
	return m
}

func (s *Server) deckStackJSON(p *deckPeople, l *store.List, cards []store.Card, labels map[int64]store.Label) map[string]any {
	cs := []any{}
	last := l.UpdatedAt
	parts := []string{"stack", strconv.FormatInt(l.ID, 10), l.UpdatedAt}
	for i := range cards {
		cs = append(cs, s.deckCardJSON(p, &cards[i], labels, nil))
		if cards[i].UpdatedAt > last {
			last = cards[i].UpdatedAt
		}
		parts = append(parts, cards[i].UpdatedAt)
	}
	return map[string]any{"title": l.Title, "boardId": l.BoardID, "deletedAt": unixOf(l.DeletedAt), "lastModified": unixOf(last), "cards": cs,
		"order": l.Position, "id": l.ID, "ETag": etagOf(parts...)}
}

// ---- lookups ----

func (s *Server) deckBoard(w http.ResponseWriter, r *http.Request, edit bool) *store.Board {
	b, err := s.store.BoardFor(r.Context(), actorID(r.Context()), idParam(r, "boardId"))
	if err != nil {
		deckNotFound(w)
		return nil
	}
	if edit && !b.CanEdit() {
		deckErr(w, http.StatusForbidden, "Permission denied")
		return nil
	}
	return b
}

func (s *Server) deckStack(w http.ResponseWriter, r *http.Request, edit bool) (*store.List, *store.Board) {
	b := s.deckBoard(w, r, edit)
	if b == nil {
		return nil, nil
	}
	l, err := s.store.ListByID(r.Context(), idParam(r, "stackId"))
	if err != nil || l.BoardID != b.ID || l.DeletedAt != "" {
		deckNotFound(w)
		return nil, nil
	}
	return l, b
}

// deckCard finds the card on the board; the stack in the path may be stale
// (clients keep the old one after a move), so only the board must match.
func (s *Server) deckCard(w http.ResponseWriter, r *http.Request, edit bool) (*store.Card, *store.Board) {
	b := s.deckBoard(w, r, edit)
	if b == nil {
		return nil, nil
	}
	c, err := s.store.CardByID(r.Context(), idParam(r, "cardId"))
	if err != nil || c.BoardID != b.ID || c.DeletedAt != "" {
		deckNotFound(w)
		return nil, nil
	}
	return c, b
}

// ---- status and capabilities ----

func (s *Server) handleStatusPHP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"installed": true, "maintenance": false, "needsDbUpgrade": false, "version": "28.0.0.0",
		"versionstring": "28.0.0", "edition": "", "productname": "Pocket Boards", "extendedSupport": false})
}

func (s *Server) ocsCapabilities(w http.ResponseWriter, r *http.Request) {
	ocsOK(w, r, map[string]any{
		"version": map[string]any{"major": 28, "minor": 0, "micro": 0, "string": "28.0.0", "edition": "", "extendedSupport": false},
		"capabilities": map[string]any{
			"core": map[string]any{"pollinterval": 60, "webdav-root": "remote.php/webdav"},
			"deck": map[string]any{"version": "1.12.0", "canCreateBoards": true, "apiVersions": []string{"1.0", "1.1"}},
			"theming": map[string]any{"name": "Pocket Boards", "url": s.cfg.BaseURL, "slogan": "Boards, lists and cards", "color": "#0c66e4",
				"color-text": "#ffffff", "background": "#0c66e4", "background-plain": true, "background-default": true, "logo": strings.TrimRight(s.cfg.BaseURL, "/") + "/favicon.svg"},
		},
	})
}

func (s *Server) ocsUser(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ocsOK(w, r, map[string]any{"id": u.Email, "enabled": true, "displayname": firstNonEmpty(u.Name, u.Email), "display-name": firstNonEmpty(u.Name, u.Email),
		"email": u.Email, "groups": []string{}, "language": "en", "locale": ""})
}

func (s *Server) ocsDeckConfig(w http.ResponseWriter, r *http.Request) {
	ocsOK(w, r, map[string]any{"calendar": true, "cardDetailsInModal": true, "cardIdBadge": false, "groupLimit": []any{}})
}

// ---- boards ----

func (s *Server) deckListBoards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	live, err := s.store.ListBoards(ctx, uid, store.BoardFilter{})
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	bin, err := s.store.ListBoards(ctx, uid, store.BoardFilter{Deleted: true})
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, b := range bin {
		if b.Role == store.RoleOwner {
			live = append(live, b) // Deck lists your deleted boards (for undo)
		}
	}
	since := ifModifiedSince(r)
	p := s.deckPeople(ctx)
	out := []any{}
	parts := []string{"boards"}
	for i := range live {
		b := &live[i]
		if !since.IsZero() && unixOf(b.UpdatedAt) <= since.Unix() {
			continue
		}
		j, err := s.deckBoardJSON(ctx, p, b)
		if err != nil {
			deckErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		parts = append(parts, strconv.FormatInt(b.ID, 10), b.UpdatedAt, b.Role)
		out = append(out, j)
	}
	if notModified(w, r, etagOf(parts...)) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckGetBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.store.BoardForAny(ctx, actorID(ctx), idParam(r, "boardId"))
	if err != nil || (b.DeletedAt != "" && b.Role != store.RoleOwner) {
		deckNotFound(w)
		return
	}
	j, err := s.deckBoardJSON(ctx, s.deckPeople(ctx), b)
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if notModified(w, r, j["ETag"].(string)) {
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// deckDefaultLabels are the four labels Deck gives a new board.
var deckDefaultLabels = []templateLabel{{"Finished", "31cc7c"}, {"To review", "317ccc"}, {"Action needed", "ff7a66"}, {"Later", "f1db50"}}

func (s *Server) deckCreateBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in := readDeckBody(r)
	title := strings.TrimSpace(in.str("title"))
	if title == "" {
		deckErr(w, http.StatusBadRequest, "title must be provided")
		return
	}
	color := cleanColor(in.str("color"))
	if !hexColor.MatchString(color) {
		color = "0c66e4"
	}
	b := store.Board{Title: excerpt(title, 100), Color: color, Source: "deck"}
	if err := s.store.CreateBoard(ctx, actorID(ctx), &b); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, l := range deckDefaultLabels {
		lb := store.Label{BoardID: b.ID, Title: l.Title, Color: l.Color}
		_ = s.store.CreateLabel(ctx, &lb)
	}
	s.store.AddActivity(ctx, b.ID, nil, actorID(ctx), "board_created", map[string]any{"title": b.Title}, "deck")
	nb, _ := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	j, _ := s.deckBoardJSON(ctx, s.deckPeople(ctx), nb)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckUpdateBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckBoard(w, r, true)
	if b == nil {
		return
	}
	in := readDeckBody(r)
	if t := strings.TrimSpace(in.str("title")); t != "" {
		b.Title = excerpt(t, 100)
	}
	if in.has("color") {
		if c := cleanColor(in.str("color")); hexColor.MatchString(c) {
			b.Color = c
		}
	}
	kind := "board_updated"
	if in.has("archived") && in.boolean("archived") != b.Archived {
		b.Archived = in.boolean("archived")
		kind = map[bool]string{true: "board_archived", false: "board_unarchived"}[b.Archived]
	}
	if err := s.store.UpdateBoard(ctx, b); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, kind, map[string]any{"title": b.Title})
	nb, _ := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	j, _ := s.deckBoardJSON(ctx, s.deckPeople(ctx), nb)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckDeleteBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckBoard(w, r, false)
	if b == nil {
		return
	}
	if b.Role != store.RoleOwner {
		deckErr(w, http.StatusForbidden, "Permission denied")
		return
	}
	if err := s.store.TrashBoard(ctx, b.ID, true); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.AddActivity(ctx, b.ID, nil, actorID(ctx), "board_deleted", map[string]any{"title": b.Title}, "deck")
	s.hub.send(liveEvent{BoardID: b.ID, By: actorID(ctx), Kind: "board_deleted"})
	nb, _ := s.store.BoardForAny(ctx, actorID(ctx), b.ID)
	j, _ := s.deckBoardJSON(ctx, s.deckPeople(ctx), nb)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckUndoDeleteBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.store.BoardForAny(ctx, actorID(ctx), idParam(r, "boardId"))
	if err != nil {
		deckNotFound(w)
		return
	}
	if b.Role != store.RoleOwner {
		deckErr(w, http.StatusForbidden, "Permission denied")
		return
	}
	if err := s.store.TrashBoard(ctx, b.ID, false); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "board_restored", map[string]any{"title": b.Title})
	nb, _ := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	j, _ := s.deckBoardJSON(ctx, s.deckPeople(ctx), nb)
	writeJSON(w, http.StatusOK, j)
}

// ---- sharing (ACL) ----

func (s *Server) deckOwnerBoard(w http.ResponseWriter, r *http.Request) *store.Board {
	b := s.deckBoard(w, r, false)
	if b == nil {
		return nil
	}
	if b.Role != store.RoleOwner {
		deckErr(w, http.StatusForbidden, "Permission denied")
		return nil
	}
	return b
}

func (s *Server) deckListACL(w http.ResponseWriter, r *http.Request) {
	b := s.deckBoard(w, r, false)
	if b == nil {
		return
	}
	ms, _ := s.store.Members(r.Context(), b.ID)
	out := []any{}
	for _, m := range ms {
		if m.Role != store.RoleOwner {
			out = append(out, deckACLJSON(b.ID, m))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckAddACL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckOwnerBoard(w, r)
	if b == nil {
		return
	}
	in := readDeckBody(r)
	if t, ok := in.num("type"); ok && t != 0 {
		deckErr(w, http.StatusBadRequest, "only sharing with people is supported")
		return
	}
	u, err := s.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(in.str("participant"))))
	if err != nil {
		deckErr(w, http.StatusBadRequest, "there is nobody with that address on this box")
		return
	}
	if u.ID == b.OwnerID {
		deckErr(w, http.StatusBadRequest, "that is the board's owner")
		return
	}
	role := store.RoleViewer
	if in.boolean("permissionEdit") {
		role = store.RoleEditor
	}
	if err := s.store.SetMember(ctx, b.ID, u.ID, role); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "member_added", map[string]any{"user_id": u.ID, "name": firstNonEmpty(u.Name, u.Email), "role": role})
	go s.notifyShared(u.ID, b, userFrom(ctx))
	writeJSON(w, http.StatusOK, []any{deckACLJSON(b.ID, store.Member{UserID: u.ID, Name: u.Name, Email: u.Email, Role: role})})
}

func (s *Server) deckACLMember(w http.ResponseWriter, r *http.Request, b *store.Board) *store.Member {
	ms, _ := s.store.Members(r.Context(), b.ID)
	id := idParam(r, "aclId")
	for _, m := range ms {
		if m.UserID == id && m.Role != store.RoleOwner {
			return &m
		}
	}
	deckNotFound(w)
	return nil
}

func (s *Server) deckUpdateACL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckOwnerBoard(w, r)
	if b == nil {
		return
	}
	m := s.deckACLMember(w, r, b)
	if m == nil {
		return
	}
	in := readDeckBody(r)
	m.Role = store.RoleViewer
	if in.boolean("permissionEdit") {
		m.Role = store.RoleEditor
	}
	if err := s.store.SetMember(ctx, b.ID, m.UserID, m.Role); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "member_role", map[string]any{"user_id": m.UserID, "role": m.Role})
	writeJSON(w, http.StatusOK, deckACLJSON(b.ID, *m))
}

func (s *Server) deckDeleteACL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckOwnerBoard(w, r)
	if b == nil {
		return
	}
	m := s.deckACLMember(w, r, b)
	if m == nil {
		return
	}
	if err := s.store.RemoveMember(ctx, b.ID, m.UserID); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "member_removed", map[string]any{"user_id": m.UserID})
	writeJSON(w, http.StatusOK, deckACLJSON(b.ID, *m))
}

// ---- labels ----

func (s *Server) deckListLabels(w http.ResponseWriter, r *http.Request) {
	b := s.deckBoard(w, r, false)
	if b == nil {
		return
	}
	ls, _ := s.store.Labels(r.Context(), b.ID)
	out := []any{}
	for _, l := range ls {
		out = append(out, deckLabelJSON(l, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckLabel(w http.ResponseWriter, r *http.Request, edit bool) (*store.Label, *store.Board) {
	b := s.deckBoard(w, r, edit)
	if b == nil {
		return nil, nil
	}
	l, err := s.store.LabelByID(r.Context(), idParam(r, "labelId"))
	if err != nil || l.BoardID != b.ID {
		deckNotFound(w)
		return nil, nil
	}
	return l, b
}

func (s *Server) deckGetLabel(w http.ResponseWriter, r *http.Request) {
	l, _ := s.deckLabel(w, r, false)
	if l == nil {
		return
	}
	writeJSON(w, http.StatusOK, deckLabelJSON(*l, nil))
}

func (s *Server) deckCreateLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckBoard(w, r, true)
	if b == nil {
		return
	}
	in := readDeckBody(r)
	l := store.Label{BoardID: b.ID, Title: strings.TrimSpace(in.str("title")), Color: cleanColor(in.str("color"))}
	if !hexColor.MatchString(l.Color) {
		l.Color = "8590a2"
	}
	if err := s.store.CreateLabel(ctx, &l); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusOK, deckLabelJSON(l, nil))
}

func (s *Server) deckUpdateLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.deckLabel(w, r, true)
	if l == nil {
		return
	}
	in := readDeckBody(r)
	if in.has("title") {
		l.Title = strings.TrimSpace(in.str("title"))
	}
	if c := cleanColor(in.str("color")); hexColor.MatchString(c) {
		l.Color = c
	}
	if err := s.store.UpdateLabel(ctx, l); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusOK, deckLabelJSON(*l, nil))
}

func (s *Server) deckDeleteLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.deckLabel(w, r, true)
	if l == nil {
		return
	}
	if err := s.store.DeleteLabel(ctx, l); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusOK, deckLabelJSON(*l, nil))
}

// ---- stacks ----

func (s *Server) deckStacks(w http.ResponseWriter, r *http.Request, archived bool) {
	ctx := r.Context()
	b := s.deckBoard(w, r, false)
	if b == nil {
		return
	}
	lists, err := s.store.Lists(ctx, b.ID, false)
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cards, err := s.store.BoardCards(ctx, b.ID, archived)
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, _ := s.store.Labels(ctx, b.ID)
	lm := labelMap(labels)
	byList := map[int64][]store.Card{}
	for _, c := range cards {
		byList[c.ListID] = append(byList[c.ListID], c)
	}
	since := ifModifiedSince(r)
	p := s.deckPeople(ctx)
	out := []any{}
	parts := []string{"stacks", strconv.FormatBool(archived)}
	for i := range lists {
		j := s.deckStackJSON(p, &lists[i], byList[lists[i].ID], lm)
		if !since.IsZero() && j["lastModified"].(int64) <= since.Unix() {
			continue
		}
		parts = append(parts, j["ETag"].(string))
		out = append(out, j)
	}
	if notModified(w, r, etagOf(parts...)) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckListStacks(w http.ResponseWriter, r *http.Request)     { s.deckStacks(w, r, false) }
func (s *Server) deckArchivedStacks(w http.ResponseWriter, r *http.Request) { s.deckStacks(w, r, true) }

func (s *Server) deckOneStack(ctx context.Context, l *store.List) (map[string]any, error) {
	cards, err := s.store.ListCards(ctx, l.ID, false)
	if err != nil {
		return nil, err
	}
	labels, _ := s.store.Labels(ctx, l.BoardID)
	return s.deckStackJSON(s.deckPeople(ctx), l, cards, labelMap(labels)), nil
}

func (s *Server) deckGetStack(w http.ResponseWriter, r *http.Request) {
	l, _ := s.deckStack(w, r, false)
	if l == nil {
		return
	}
	j, err := s.deckOneStack(r.Context(), l)
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckCreateStack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.deckBoard(w, r, true)
	if b == nil {
		return
	}
	in := readDeckBody(r)
	title := strings.TrimSpace(in.str("title"))
	if title == "" {
		deckErr(w, http.StatusBadRequest, "title must be provided")
		return
	}
	pos := -1
	if n, ok := in.num("order"); ok && n >= 0 && n < 1000 {
		pos = int(n)
	}
	l := store.List{BoardID: b.ID, Title: excerpt(title, 100)}
	if err := s.store.CreateList(ctx, &l, pos); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "list_created", map[string]any{"list": l.Title})
	j, _ := s.deckOneStack(ctx, &l)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckUpdateStack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.deckStack(w, r, true)
	if l == nil {
		return
	}
	in := readDeckBody(r)
	if t := strings.TrimSpace(in.str("title")); t != "" {
		l.Title = excerpt(t, 100)
	}
	if err := s.store.UpdateList(ctx, l); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, ok := in.num("order"); ok && int(n) != l.Position && n >= 0 {
		if err := s.store.MoveList(ctx, l.ID, int(n)); err != nil {
			deckErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.changed(ctx, b, nil, "list_updated", map[string]any{"list": l.Title})
	nl, _ := s.store.ListByID(ctx, l.ID)
	j, _ := s.deckOneStack(ctx, nl)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deckDeleteStack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.deckStack(w, r, true)
	if l == nil {
		return
	}
	if err := s.store.TrashList(ctx, l.ID, true); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, nil, "list_deleted", map[string]any{"list": l.Title})
	nl, _ := s.store.ListByID(ctx, l.ID)
	writeJSON(w, http.StatusOK, s.deckStackJSON(s.deckPeople(ctx), nl, nil, nil))
}

// ---- cards ----

func (s *Server) deckCardOut(ctx context.Context, c *store.Card, withAtts bool) map[string]any {
	labels, _ := s.store.Labels(ctx, c.BoardID)
	var atts []store.Attachment
	if withAtts {
		atts, _ = s.store.Attachments(ctx, c.ID)
		if atts == nil {
			atts = []store.Attachment{}
		}
	}
	return s.deckCardJSON(s.deckPeople(ctx), c, labelMap(labels), atts)
}

func (s *Server) deckListCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, _ := s.deckStack(w, r, false)
	if l == nil {
		return
	}
	cards, _ := s.store.ListCards(ctx, l.ID, false)
	labels, _ := s.store.Labels(ctx, l.BoardID)
	lm := labelMap(labels)
	p := s.deckPeople(ctx)
	out := []any{}
	for i := range cards {
		out = append(out, s.deckCardJSON(p, &cards[i], lm, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckGetCard(w http.ResponseWriter, r *http.Request) {
	c, _ := s.deckCard(w, r, false)
	if c == nil {
		return
	}
	j := s.deckCardOut(r.Context(), c, true)
	if notModified(w, r, j["ETag"].(string)) {
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// deckDue reads a Deck duedate (ISO 8601, null or empty = none).
func deckDue(in deckBody, k string) string {
	if in.null(k) {
		return ""
	}
	return in.str(k)
}

func (s *Server) deckCreateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.deckStack(w, r, true)
	if l == nil {
		return
	}
	in := readDeckBody(r)
	ci := cardInput{Title: excerpt(strings.TrimSpace(in.str("title")), 255), DueAt: deckDue(in, "duedate")}
	if !in.null("description") {
		ci.Description = in.str("description")
	}
	if n, ok := in.num("order"); ok && n >= 0 {
		p := int(n)
		ci.Position = &p
	}
	c, status, msg := s.newCard(ctx, b, l, ci)
	if msg != "" {
		deckErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, s.deckCardOut(ctx, c, false))
}

func (s *Server) deckUpdateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.deckCard(w, r, true)
	if c == nil {
		return
	}
	in := readDeckBody(r)
	ci := cardInput{}
	keys := map[string]bool{}
	if t := strings.TrimSpace(in.str("title")); t != "" {
		ci.Title, keys["title"] = excerpt(t, 255), true
	}
	if in.has("description") {
		if !in.null("description") {
			ci.Description = in.str("description")
		}
		keys["description"] = true
	}
	if in.has("duedate") {
		ci.DueAt, keys["due_at"] = deckDue(in, "duedate"), true
	}
	if in.has("archived") {
		ci.Archived, keys["archived"] = in.boolean("archived"), true
	}
	if in.has("done") {
		ci.Done, keys["done"] = !in.null("done") && in.str("done") != "false", true
	}
	if sid, ok := in.num("stackId"); ok && sid > 0 && sid != c.ListID {
		ci.ListID, keys["list_id"] = sid, true
		if n, ok := in.num("order"); ok && n >= 0 {
			p := int(n)
			ci.Position, keys["position"] = &p, true
		}
	}
	nc, status, msg := s.updateCard(ctx, b, c, ci, keys)
	if msg != "" {
		deckErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, s.deckCardOut(ctx, nc, false))
}

func (s *Server) deckDeleteCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.deckCard(w, r, true)
	if c == nil {
		return
	}
	if err := s.store.TrashCard(ctx, c, true); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, c, "card_deleted", nil)
	nc, _ := s.store.CardByID(ctx, c.ID)
	writeJSON(w, http.StatusOK, s.deckCardOut(ctx, nc, false))
}

func (s *Server) deckCardLabel(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, b := s.deckCard(w, r, true)
		if c == nil {
			return
		}
		lid, _ := readDeckBody(r).num("labelId")
		l, err := s.store.LabelByID(ctx, lid)
		if err != nil || l.BoardID != b.ID {
			deckErr(w, http.StatusBadRequest, "That label does not belong to this board")
			return
		}
		if err := s.store.SetCardLabel(ctx, c, lid, on); err != nil {
			deckErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.changed(ctx, b, c, "", nil)
		writeJSON(w, http.StatusOK, nil)
	}
}

// deckMemberByUID finds someone on the board by uid (email), or by name as a fallback.
func (s *Server) deckMemberByUID(ctx context.Context, boardID int64, uid string) *store.Member {
	ms, _ := s.store.Members(ctx, boardID)
	uid = strings.ToLower(strings.TrimSpace(uid))
	for _, m := range ms {
		if strings.ToLower(m.Email) == uid {
			return &m
		}
	}
	for _, m := range ms {
		if strings.ToLower(m.Name) == uid || strings.ToLower(strings.SplitN(m.Email, "@", 2)[0]) == uid {
			return &m
		}
	}
	return nil
}

func (s *Server) deckCardUser(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, b := s.deckCard(w, r, true)
		if c == nil {
			return
		}
		m := s.deckMemberByUID(ctx, b.ID, readDeckBody(r).str("userId"))
		if m == nil {
			deckErr(w, http.StatusBadRequest, "The user is not part of the board")
			return
		}
		changed, err := s.store.SetCardMember(ctx, c, m.UserID, on)
		if err != nil {
			deckErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !changed {
			if on {
				deckErr(w, http.StatusBadRequest, "The user is already assigned to the card")
			} else {
				deckErr(w, http.StatusBadRequest, "No user was assigned to the card")
			}
			return
		}
		kind := map[bool]string{true: "card_assigned", false: "card_unassigned"}[on]
		s.changed(ctx, b, c, kind, map[string]any{"user_id": m.UserID, "name": firstNonEmpty(m.Name, m.Email)})
		if on {
			l, _ := s.store.ListByID(ctx, c.ListID)
			s.publishCard(ctx, "boards.card_assigned", b, l, c, map[string]any{"assignee_id": m.UserID, "assignee": firstNonEmpty(m.Name, m.Email)})
			if m.UserID != actorID(ctx) {
				go s.notifyAssigned(m.UserID, b, c, userFrom(ctx))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": m.UserID, "participant": deckUserObj(m.Email, m.Name), "cardId": c.ID, "type": 0})
	}
}

func (s *Server) deckReorderCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.deckCard(w, r, true)
	if c == nil {
		return
	}
	in := readDeckBody(r)
	sid, ok := in.num("stackId")
	if !ok || sid <= 0 {
		sid = c.ListID
	}
	order, _ := in.num("order")
	if order < 0 {
		order = 0
	}
	p := int(order)
	nc, status, msg := s.updateCard(ctx, b, c, cardInput{ListID: sid, Position: &p}, map[string]bool{"list_id": true, "position": true})
	if msg != "" {
		deckErr(w, status, msg)
		return
	}
	cards, _ := s.store.ListCards(ctx, nc.ListID, false)
	labels, _ := s.store.Labels(ctx, nc.BoardID)
	lm := labelMap(labels)
	pp := s.deckPeople(ctx)
	out := []any{}
	for i := range cards {
		out = append(out, s.deckCardJSON(pp, &cards[i], lm, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckCardFlag(flag string, on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, b := s.deckCard(w, r, true)
		if c == nil {
			return
		}
		ci := cardInput{}
		keys := map[string]bool{flag: true}
		if flag == "archived" {
			ci.Archived = on
		} else {
			ci.Done = on
		}
		nc, status, msg := s.updateCard(ctx, b, c, ci, keys)
		if msg != "" {
			deckErr(w, status, msg)
			return
		}
		writeJSON(w, http.StatusOK, s.deckCardOut(ctx, nc, false))
	}
}

// ---- attachments ----

func (s *Server) deckListAttachments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, _ := s.deckCard(w, r, false)
	if c == nil {
		return
	}
	atts, _ := s.store.Attachments(ctx, c.ID)
	p := s.deckPeople(ctx)
	out := []any{}
	for i := range atts {
		out = append(out, s.deckAttachmentJSON(p, &atts[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deckUploadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.deckCard(w, r, true)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		deckErr(w, http.StatusBadRequest, "send the file as multipart form data (field \"file\")")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		deckErr(w, http.StatusBadRequest, "No file uploaded")
		return
	}
	defer f.Close()
	a, err := s.saveAttachment(ctx, c, hdr.Filename, hdr.Header.Get("Content-Type"), f)
	if err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, c, "attachment_added", map[string]any{"filename": a.Filename})
	writeJSON(w, http.StatusOK, s.deckAttachmentJSON(s.deckPeople(ctx), a))
}

func (s *Server) deckAttachment(w http.ResponseWriter, r *http.Request, edit, deleted bool) (*store.Attachment, *store.Card, *store.Board) {
	c, b := s.deckCard(w, r, edit)
	if c == nil {
		return nil, nil, nil
	}
	a, err := s.store.AttachmentByID(r.Context(), idParam(r, "attachmentId"))
	if err != nil || a.CardID != c.ID || (a.DeletedAt != "") != deleted {
		deckNotFound(w)
		return nil, nil, nil
	}
	return a, c, b
}

func (s *Server) deckGetAttachment(w http.ResponseWriter, r *http.Request) {
	a, _, _ := s.deckAttachment(w, r, false, false)
	if a == nil {
		return
	}
	s.serveAttachment(w, r, a)
}

func (s *Server) deckDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, c, b := s.deckAttachment(w, r, true, false)
	if a == nil {
		return
	}
	if err := s.store.TrashAttachment(ctx, a.ID, true); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.TouchBoard(ctx, b.ID)
	s.changed(ctx, b, c, "attachment_deleted", map[string]any{"filename": a.Filename})
	na, _ := s.store.AttachmentByID(ctx, a.ID)
	writeJSON(w, http.StatusOK, s.deckAttachmentJSON(s.deckPeople(ctx), na))
}

func (s *Server) deckRestoreAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, c, b := s.deckAttachment(w, r, true, true)
	if a == nil {
		return
	}
	if err := s.store.TrashAttachment(ctx, a.ID, false); err != nil {
		deckErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.TouchBoard(ctx, b.ID)
	s.changed(ctx, b, c, "attachment_added", map[string]any{"filename": a.Filename})
	na, _ := s.store.AttachmentByID(ctx, a.ID)
	writeJSON(w, http.StatusOK, s.deckAttachmentJSON(s.deckPeople(ctx), na))
}

// ---- comments (OCS) ----

func (s *Server) ocsCard(w http.ResponseWriter, r *http.Request) (*store.Card, *store.Board) {
	ctx := r.Context()
	c, err := s.store.CardByID(ctx, idParam(r, "cardId"))
	if err != nil || c.DeletedAt != "" {
		ocsErr(w, r, http.StatusNotFound, "Not found")
		return nil, nil
	}
	b, err := s.store.BoardFor(ctx, actorID(ctx), c.BoardID)
	if err != nil {
		ocsErr(w, r, http.StatusNotFound, "Not found")
		return nil, nil
	}
	return c, b
}

func (s *Server) ocsCommentJSON(p *deckPeople, cm *store.Comment, byID map[int64]*store.Comment) map[string]any {
	actor, name := "deleted_users", firstNonEmpty(cm.AuthorName, "Deleted user")
	if cm.UserID != nil {
		if u := p.user(*cm.UserID); u != nil {
			actor, name = u.Email, firstNonEmpty(u.Name, u.Email)
		}
	}
	j := map[string]any{"id": cm.ID, "objectId": cm.CardID, "message": cm.Message, "actorId": actor, "actorType": "users", "actorDisplayName": name,
		"creationDateTime": deckTime(cm.CreatedAt), "mentions": []any{}}
	if cm.ParentID != nil && byID != nil {
		if parent, ok := byID[*cm.ParentID]; ok {
			j["replyTo"] = s.ocsCommentJSON(p, parent, nil)
		}
	}
	return j
}

func (s *Server) ocsListComments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, _ := s.ocsCard(w, r)
	if c == nil {
		return
	}
	cs, err := s.store.Comments(ctx, c.ID)
	if err != nil {
		ocsErr(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	byID := map[int64]*store.Comment{}
	for i := range cs {
		byID[cs[i].ID] = &cs[i]
	}
	// Newest first, like Deck.
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].ID > cs[j].ID })
	limit, offset := queryInt(r, "limit"), queryInt(r, "offset")
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	p := s.deckPeople(ctx)
	out := []any{}
	for i := offset; i < len(cs) && len(out) < limit; i++ {
		out = append(out, s.ocsCommentJSON(p, &cs[i], byID))
	}
	ocsOK(w, r, out)
}

func (s *Server) ocsCreateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.ocsCard(w, r)
	if c == nil {
		return
	}
	in := readDeckBody(r)
	var parent *int64
	if pid, ok := in.num("parentId"); ok && pid > 0 {
		parent = &pid
	}
	cm, status, msg := s.addComment(ctx, b, c, strings.TrimSpace(in.str("message")), parent)
	if msg != "" {
		ocsErr(w, r, status, msg)
		return
	}
	byID := map[int64]*store.Comment{}
	if parent != nil {
		if pc, err := s.store.CommentByID(ctx, *parent); err == nil {
			byID[pc.ID] = pc
		}
	}
	ocsOK(w, r, s.ocsCommentJSON(s.deckPeople(ctx), cm, byID))
}

func (s *Server) ocsOwnComment(w http.ResponseWriter, r *http.Request) (*store.Comment, *store.Card, *store.Board) {
	ctx := r.Context()
	c, b := s.ocsCard(w, r)
	if c == nil {
		return nil, nil, nil
	}
	cm, err := s.store.CommentByID(ctx, idParam(r, "commentId"))
	if err != nil || cm.CardID != c.ID {
		ocsErr(w, r, http.StatusNotFound, "Not found")
		return nil, nil, nil
	}
	if cm.UserID == nil || *cm.UserID != actorID(ctx) {
		ocsErr(w, r, http.StatusForbidden, "Only authors can edit their comment")
		return nil, nil, nil
	}
	return cm, c, b
}

func (s *Server) ocsUpdateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cm, c, b := s.ocsOwnComment(w, r)
	if cm == nil {
		return
	}
	msg := strings.TrimSpace(readDeckBody(r).str("message"))
	if msg == "" {
		ocsErr(w, r, http.StatusBadRequest, "The message must not be empty")
		return
	}
	cm.Message = msg
	if err := s.store.UpdateComment(ctx, cm); err != nil {
		ocsErr(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, c, "", nil)
	ncm, _ := s.store.CommentByID(ctx, cm.ID)
	ocsOK(w, r, s.ocsCommentJSON(s.deckPeople(ctx), ncm, nil))
}

func (s *Server) ocsDeleteComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cm, c, b := s.ocsOwnComment(w, r)
	if cm == nil {
		return
	}
	if err := s.store.DeleteComment(ctx, cm.ID); err != nil {
		ocsErr(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.changed(ctx, b, c, "", nil)
	ocsOK(w, r, []any{})
}

// deckSafeID keeps an external id short and printable.
func deckSafeID(parts ...any) string {
	ss := make([]string, len(parts))
	for i, p := range parts {
		ss[i] = fmt.Sprint(p)
	}
	return strings.Join(ss, ":")
}
