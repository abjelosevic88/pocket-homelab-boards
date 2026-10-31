package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	if st.ICSToken == "" && u != nil {
		st.ICSToken = store.RandomToken(24)
		_ = s.store.SaveUserSettings(ctx, u.ID, st)
	}
	ncMode := s.connMode(st.NextcloudMode, st.NextcloudPassword)
	ncVia, ncDetail := "", ""
	switch ncMode {
	case modeDirect:
		if st.NextcloudURL != "" && st.NextcloudUser != "" && st.NextcloudPassword != "" {
			ncVia = modeDirect
		} else {
			ncDetail = "add the address, user and app password"
		}
	case modeCore:
		if !s.core.Configured() {
			ncDetail = "there is no Pocket Core on this box"
		} else if u != nil {
			if _, err := s.core.ServiceFor(ctx, u.Email, "nextcloud"); err == nil {
				ncVia = modeCore
			} else {
				ncDetail = "add your Nextcloud account in Pocket Core (Settings → Services)"
			}
		}
	default:
		ncDetail = "switched off"
	}
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart,
		"default_view": st.DefaultView, "card_density": st.CardDensity, "show_done_cards": st.ShowDoneCards, "confirm_delete": st.ConfirmDelete, "due_time_default": st.DueTimeDefault,
		"notify_due": st.NotifyDue, "remind_before_min": st.RemindBeforeMin, "notify_assigned": st.NotifyAssigned, "notify_comments": st.NotifyComments,
		"notify_digest": st.NotifyDigest, "digest_hour": st.DigestHour,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"ics_url":   s.icsURL(st),
		"tasks_url": st.TasksURL, "tasks_token": mask(st.TasksToken), "tasks_public_url": st.TasksPublicURL,
		"time_url": st.TimeURL, "time_token": mask(st.TimeToken), "time_public_url": st.TimePublicURL,
		"nextcloud_mode": ncMode, "nextcloud_url": st.NextcloudURL, "nextcloud_user": st.NextcloudUser, "nextcloud_password": mask(st.NextcloudPassword),
		"nextcloud_via": ncVia, "nextcloud_detail": ncDetail, "nextcloud_configured": ncVia != "",
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, _, _, public := connSettings(st, a)
		if public == "" && via != "" {
			public = s.resolveSibling(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	intIn := func(k string, dst *int, v, lo, hi int) bool {
		if !keys[k] {
			return true
		}
		if v < lo || v > hi {
			bad(fmt.Sprintf("%s is %d to %d", k, lo, hi))
			return false
		}
		*dst = v
		return true
	}
	if !intIn("remind_before_min", &cur.RemindBeforeMin, in.RemindBeforeMin, 0, 7*24*60) || !intIn("digest_hour", &cur.DigestHour, in.DigestHour, 0, 23) {
		return
	}
	pick := func(k string, dst *string, v string, allowed []string) bool {
		if !keys[k] {
			return true
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
				return true
			}
		}
		bad(fmt.Sprintf("%s must be one of %s", k, strings.Join(allowed, ", ")))
		return false
	}
	if !pick("default_view", &cur.DefaultView, in.DefaultView, []string{"home", "mine"}) || !pick("card_density", &cur.CardDensity, in.CardDensity, []string{"comfortable", "compact"}) {
		return
	}
	if keys["due_time_default"] {
		if !hhmm.MatchString(in.DueTimeDefault) {
			bad("the default due time is HH:MM")
			return
		}
		cur.DueTimeDefault = in.DueTimeDefault
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boolIn("show_done_cards", &cur.ShowDoneCards, in.ShowDoneCards)
	boolIn("confirm_delete", &cur.ConfirmDelete, in.ConfirmDelete)
	boolIn("notify_due", &cur.NotifyDue, in.NotifyDue)
	boolIn("notify_assigned", &cur.NotifyAssigned, in.NotifyAssigned)
	boolIn("notify_comments", &cur.NotifyComments, in.NotifyComments)
	boolIn("notify_digest", &cur.NotifyDigest, in.NotifyDigest)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	url("tasks_url", &cur.TasksURL, in.TasksURL)
	secret("tasks_token", &cur.TasksToken, in.TasksToken)
	url("tasks_public_url", &cur.TasksPublicURL, in.TasksPublicURL)
	url("time_url", &cur.TimeURL, in.TimeURL)
	secret("time_token", &cur.TimeToken, in.TimeToken)
	url("time_public_url", &cur.TimePublicURL, in.TimePublicURL)
	url("nextcloud_url", &cur.NextcloudURL, in.NextcloudURL)
	text("nextcloud_user", &cur.NextcloudUser, in.NextcloudUser)
	secret("nextcloud_password", &cur.NextcloudPassword, in.NextcloudPassword)
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	mode := func(k string, dst *string, v string) {
		if keys[k] && (v == modeCore || v == modeDirect || v == modeOff) {
			*dst = v
		}
	}
	mode("tasks_mode", &cur.TasksMode, in.TasksMode)
	mode("time_mode", &cur.TimeMode, in.TimeMode)
	mode("nextcloud_mode", &cur.NextcloudMode, in.NextcloudMode)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for _, k := range modeKeys {
		if keys[k] {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		User   string `json:"user"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				msg = fmt.Sprintf("%s %s, %s", a.name, v, how)
				if a.slug == "time" {
					if t, terr := conn.client.Timer(ctx); terr == nil && t.Running != nil {
						msg += ", a timer is running"
					}
				}
			}
		}
	} else if in.Which == "nextcloud" {
		msg, err = s.testNextcloud(ctx, uid, urlOr(st.NextcloudURL), firstNonEmpty(in.User, st.NextcloudUser), keyOr(st.NextcloudPassword), in.URL != "" || in.User != "" || (in.Key != "" && !strings.Contains(in.Key, "••")))
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Boards", Body: "Notifications work. Due reminders, cards given to you and comments will arrive here.", Priority: "info", Tags: []string{"clipboard"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Boards", Body: "Notifications work.", Priority: "info", Tags: []string{"clipboard"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "saved": s.saved.Load(),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-boards-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of the boards one person owns.
type exportDoc struct {
	App      string        `json:"app"`
	Version  string        `json:"version"`
	Exported string        `json:"exported_at"`
	Boards   []exportBoard `json:"boards"`
}

type exportBoard struct {
	Board  store.Board   `json:"board"`
	Labels []store.Label `json:"labels"`
	Lists  []exportList  `json:"lists"`
}

type exportList struct {
	List  store.List   `json:"list"`
	Cards []exportCard `json:"cards"`
}

type exportCard struct {
	Card     store.Card      `json:"card"`
	Comments []store.Comment `json:"comments"`
}

func (s *Server) exportBoards(ctx context.Context, uid int64) (exportDoc, error) {
	doc := exportDoc{App: "pocket-boards", Version: Version, Exported: store.Now(), Boards: []exportBoard{}}
	boards, err := s.store.ListBoards(ctx, uid, store.BoardFilter{})
	if err != nil {
		return doc, err
	}
	for _, b := range boards {
		if b.Role != store.RoleOwner {
			continue
		}
		eb := exportBoard{Board: b, Lists: []exportList{}}
		eb.Labels, _ = s.store.Labels(ctx, b.ID)
		lists, _ := s.store.Lists(ctx, b.ID, false)
		for _, l := range lists {
			el := exportList{List: l, Cards: []exportCard{}}
			for _, archived := range []bool{false, true} {
				cards, _ := s.store.ListCards(ctx, l.ID, archived)
				for _, c := range cards {
					cs, _ := s.store.Comments(ctx, c.ID)
					el.Cards = append(el.Cards, exportCard{Card: c, Comments: cs})
				}
			}
			eb.Lists = append(eb.Lists, el)
		}
		doc.Boards = append(doc.Boards, eb)
	}
	return doc, nil
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	doc, err := s.exportBoards(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-boards-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleExportCSV writes one row per card on the boards the caller owns.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	doc, err := s.exportBoards(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-boards-cards.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"board", "list", "title", "description", "labels", "due_at", "start_at", "done_at", "archived", "repeat", "checklist", "comments", "created_at", "updated_at"})
	for _, b := range doc.Boards {
		names := map[int64]string{}
		for _, l := range b.Labels {
			names[l.ID] = l.Title
		}
		for _, l := range b.Lists {
			for _, ec := range l.Cards {
				c := ec.Card
				var ls []string
				for _, id := range c.Labels {
					ls = append(ls, names[id])
				}
				check := ""
				if c.ChecklistTotal > 0 {
					check = fmt.Sprintf("%d/%d", c.ChecklistDone, c.ChecklistTotal)
				}
				_ = cw.Write([]string{b.Board.Title, l.List.Title, c.Title, c.Description, strings.Join(ls, ", "), c.DueAt, c.StartAt, c.DoneAt,
					strconv.FormatBool(c.Archived), c.Repeat, check, strconv.Itoa(len(ec.Comments)), c.CreatedAt, c.UpdatedAt})
			}
		}
	}
	cw.Flush()
}

// handleImportJSON merges an export into the caller's own boards. Boards,
// lists, labels and cards keep their identity (source "import" + the
// original id), so importing the same file twice adds nothing.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-boards" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Boards export")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{}
	for _, eb := range doc.Boards {
		ext := fmt.Sprintf("%d", eb.Board.ID)
		b, err := s.store.BoardByExternal(ctx, uid, "import", ext)
		if err != nil {
			nb := store.Board{Title: firstNonEmpty(eb.Board.Title, "Imported board"), Description: eb.Board.Description, Color: eb.Board.Color, Emoji: eb.Board.Emoji,
				Archived: eb.Board.Archived, Source: "import", ExternalID: ext}
			if !hexColor.MatchString(nb.Color) {
				nb.Color = "0c66e4"
			}
			if err := s.store.CreateBoard(ctx, uid, &nb); err != nil {
				s.fail(w, err, "import")
				return
			}
			b = &nb
			counts["boards"]++
		}
		labelMap := map[int64]int64{}
		have, _ := s.store.Labels(ctx, b.ID)
		for _, l := range eb.Labels {
			lext := fmt.Sprintf("%d", l.ID)
			found := int64(0)
			for _, h := range have {
				if h.ExternalID == lext {
					found = h.ID
				}
			}
			if found == 0 {
				nl := store.Label{BoardID: b.ID, Title: l.Title, Color: l.Color, ExternalID: lext}
				if !hexColor.MatchString(nl.Color) {
					nl.Color = "8590a2"
				}
				if err := s.store.CreateLabel(ctx, &nl); err != nil {
					continue
				}
				found = nl.ID
				counts["labels"]++
			}
			labelMap[l.ID] = found
		}
		for _, el := range eb.Lists {
			lext := fmt.Sprintf("%d", el.List.ID)
			l, err := s.store.ListByExternal(ctx, b.ID, lext)
			if err != nil {
				nl := store.List{BoardID: b.ID, Title: firstNonEmpty(el.List.Title, "List"), WIPLimit: el.List.WIPLimit, IsDone: el.List.IsDone, Color: el.List.Color, ExternalID: lext}
				if err := s.store.CreateList(ctx, &nl, -1); err != nil {
					continue
				}
				l = &nl
				counts["lists"]++
			}
			for _, ec := range el.Cards {
				cext := fmt.Sprintf("%d", ec.Card.ID)
				if _, err := s.store.CardByExternal(ctx, b.ID, "import", cext); err == nil {
					counts["skipped"]++
					continue
				}
				c := ec.Card
				nc := store.Card{BoardID: b.ID, ListID: l.ID, Title: firstNonEmpty(c.Title, "Card"), Description: c.Description, StartAt: c.StartAt, DueAt: c.DueAt,
					DoneAt: c.DoneAt, Archived: c.Archived, CoverColor: c.CoverColor, Repeat: c.Repeat, Source: "import", ExternalID: cext, CreatedAt: c.CreatedAt}
				if !repeats[nc.Repeat] {
					nc.Repeat = ""
				}
				for _, x := range c.Labels {
					if id, ok := labelMap[x]; ok {
						nc.Labels = append(nc.Labels, id)
					}
				}
				if err := s.store.CreateCard(ctx, uid, &nc, -1); err != nil {
					continue
				}
				counts["cards"]++
				for _, cm := range ec.Comments {
					ncm := store.Comment{CardID: nc.ID, Message: cm.Message, Source: "import", ExternalID: fmt.Sprintf("%d", cm.ID), CreatedAt: cm.CreatedAt}
					if ncm.Message != "" && s.store.CreateComment(ctx, uid, &ncm) == nil {
						counts["comments"]++
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, counts)
}
