package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- links back into the app ----

func (s *Server) cardPath(c *store.Card) string { return fmt.Sprintf("/b/%d/c/%d", c.BoardID, c.ID) }

func (s *Server) cardURL(c *store.Card) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + s.cardPath(c)
}

// ---- events on the Pocket Core bus ----

func (s *Server) publishCard(ctx context.Context, topic string, b *store.Board, l *store.List, c *store.Card, extra map[string]any) {
	if !s.core.Configured() {
		return
	}
	p := map[string]any{"card_id": c.ID, "board_id": b.ID, "title": c.Title, "board": b.Title, "url": s.cardURL(c), "due_at": c.DueAt, "done": c.DoneAt != ""}
	if l != nil {
		p["list"], p["list_id"] = l.Title, l.ID
	}
	for k, v := range extra {
		p[k] = v
	}
	s.publish(ctx, actorID(ctx), topic, fmt.Sprintf("card:%d", c.ID), p)
}

// ---- notifications ----
//
// Everything goes to the recipient's own notification address (Settings →
// Notifications), never the sender's.

func (s *Server) notifyUser(ctx context.Context, uid int64, m notify.Message) bool {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil || st.NotifyURL == "" {
		return false
	}
	if err := notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, m); err != nil {
		s.log.Warn("notification failed", "user", uid, "err", err)
		return false
	}
	return true
}

func whoName(u *store.User) string {
	if u == nil {
		return "Someone"
	}
	return firstNonEmpty(u.Name, u.Email)
}

func (s *Server) notifyAssigned(uid int64, b *store.Board, c *store.Card, by *store.User) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, _ := s.store.GetSettings(ctx, uid)
	if !st.NotifyAssigned {
		return
	}
	body := fmt.Sprintf("%s gave you \"%s\" on %s.", whoName(by), c.Title, b.Title)
	if t, ok := parseTime(c.DueAt); ok {
		body += " Due " + t.Format("Mon 2 Jan 15:04") + "."
	}
	s.notifyUser(ctx, uid, notify.Message{Title: "A card for you", Body: body, Link: s.cardURL(c), Priority: "info", Tags: []string{"clipboard"}})
}

func (s *Server) notifyShared(uid int64, b *store.Board, by *store.User) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, _ := s.store.GetSettings(ctx, uid)
	if !st.NotifyAssigned {
		return
	}
	s.notifyUser(ctx, uid, notify.Message{Title: "A board shared with you", Body: fmt.Sprintf("%s shared the board \"%s\" with you.", whoName(by), b.Title),
		Link: strings.TrimRight(s.cfg.BaseURL, "/") + fmt.Sprintf("/b/%d", b.ID), Priority: "info", Tags: []string{"clipboard"}})
}

// notifyComment tells the people on the card (assignees, or the board's
// owner when nobody is) — not the person who wrote it.
func (s *Server) notifyComment(b *store.Board, c *store.Card, cm *store.Comment, by *store.User) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to := append([]int64(nil), c.Members...)
	if len(to) == 0 {
		to = []int64{b.OwnerID}
	}
	for _, uid := range to {
		if by != nil && uid == by.ID {
			continue
		}
		st, _ := s.store.GetSettings(ctx, uid)
		if !st.NotifyComments {
			continue
		}
		s.notifyUser(ctx, uid, notify.Message{Title: fmt.Sprintf("%s on \"%s\"", whoName(by), c.Title), Body: excerpt(cm.Message, 300), Link: s.cardURL(c), Priority: "info", Tags: []string{"speech_balloon"}})
	}
}

// reminders runs every minute: due reminders (each person's own lead time)
// and the morning digest (each person's own hour).
func (s *Server) reminders(ctx context.Context) {
	if !s.remind.TryLock() {
		return
	}
	defer s.remind.Unlock()
	now := time.Now()
	cards, err := s.store.DueToRemind(ctx, now.Add(7*24*time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		s.log.Warn("reminders", "err", err)
		return
	}
	settings := map[int64]store.Settings{}
	stFor := func(uid int64) store.Settings {
		if st, ok := settings[uid]; ok {
			return st
		}
		st, _ := s.store.GetSettings(ctx, uid)
		settings[uid] = st
		return st
	}
	boards := map[int64]*store.Board{}
	for _, c := range cards {
		due, ok := parseTime(c.DueAt)
		if !ok {
			_ = s.store.SetReminded(ctx, c.ID, c.DueAt)
			continue
		}
		b, ok := boards[c.BoardID]
		if !ok {
			var owner int64
			_ = s.store.DB.QueryRowContext(ctx, `SELECT user_id FROM boards WHERE id = ?`, c.BoardID).Scan(&owner)
			b, _ = s.store.BoardFor(ctx, owner, c.BoardID)
			boards[c.BoardID] = b
		}
		if b == nil {
			continue
		}
		to := append([]int64(nil), c.Members...)
		if len(to) == 0 {
			to = []int64{b.OwnerID}
		}
		all := true
		for _, uid := range to {
			st := stFor(uid)
			lead := time.Duration(st.RemindBeforeMin) * time.Minute
			if !st.NotifyDue {
				continue
			}
			if now.Before(due.Add(-lead)) {
				all = false
				continue
			}
			key := fmt.Sprintf("remind:%d:%d:%s", c.ID, uid, c.DueAt)
			if _, _, sent := s.store.CacheGet(ctx, key); sent {
				continue
			}
			when := "is due " + relWhen(due, now)
			if due.Before(now) {
				when = "was due " + relWhen(due, now)
			}
			s.notifyUser(ctx, uid, notify.Message{Title: c.Title, Body: fmt.Sprintf("%s (%s).", strings.ToUpper(when[:1])+when[1:], b.Title), Link: s.cardURL(&c), Priority: "info", Tags: []string{"alarm_clock"}})
			_ = s.store.CachePut(ctx, key, "1")
		}
		if all {
			_ = s.store.SetReminded(ctx, c.ID, c.DueAt)
			if !due.After(now) || due.Sub(now) < time.Hour {
				l, _ := s.store.ListByID(ctx, c.ListID)
				uctx := context.WithValue(ctx, userKey, &store.User{ID: b.OwnerID})
				s.publishCard(uctx, "boards.card_due", b, l, &c, nil)
			}
		}
	}
	s.digests(ctx, now)
}

func relWhen(t, now time.Time) string {
	d := t.Sub(now)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "now"
	case startOfDay(t).Equal(startOfDay(now)):
		return "today at " + t.Format("15:04")
	case startOfDay(t).Equal(startOfDay(now).AddDate(0, 0, 1)):
		return "tomorrow at " + t.Format("15:04")
	case startOfDay(t).Equal(startOfDay(now).AddDate(0, 0, -1)):
		return "yesterday at " + t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

// digests sends each person who wants it, once a day at their hour, what is
// overdue or due today (assigned to them, or nobody's on their own boards).
func (s *Server) digests(ctx context.Context, now time.Time) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		st, _ := s.store.GetSettings(ctx, u.ID)
		if !st.NotifyDigest || st.NotifyURL == "" || now.Hour() != st.DigestHour {
			continue
		}
		key := fmt.Sprintf("digest:%d:%s", u.ID, now.Format("2006-01-02"))
		if _, _, sent := s.store.CacheGet(ctx, key); sent {
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		cards, err := s.store.FindCards(ctx, u.ID, store.CardFilter{HasDue: true, DueTo: endOfDay(now).UTC().Format(time.RFC3339), Limit: 100})
		if err != nil {
			continue
		}
		var lines []string
		for _, c := range cards {
			if len(c.Members) > 0 && !containsID(c.Members, u.ID) {
				continue
			}
			due, _ := parseTime(c.DueAt)
			mark := due.Format("15:04")
			if due.Before(startOfDay(now)) {
				mark = "overdue"
			}
			lines = append(lines, fmt.Sprintf("• %s — %s (%s)", c.Title, mark, c.BoardTitle))
		}
		if len(lines) == 0 {
			continue
		}
		if len(lines) > 15 {
			lines = append(lines[:15], fmt.Sprintf("…and %d more", len(lines)-15))
		}
		s.notifyUser(ctx, u.ID, notify.Message{Title: fmt.Sprintf("Today: %d %s", len(lines), map[bool]string{true: "card", false: "cards"}[len(lines) == 1]),
			Body: strings.Join(lines, "\n"), Link: strings.TrimRight(s.cfg.BaseURL, "/") + "/my", Priority: "info", Tags: []string{"clipboard"}})
	}
}

// ---- Pocket Tasks ----

func (s *Server) handleCardToTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), false, false)
	if c == nil {
		return
	}
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appTasks, scopeTasksWrite)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	notes := fmt.Sprintf("From the board %s.", b.Title)
	if d := strings.TrimSpace(c.Description); d != "" {
		notes += "\n\n" + d
	}
	due := ""
	if t, ok := parseTime(c.DueAt); ok {
		due = t.Format(time.RFC3339)
	}
	t, err := conn.client.CreateTask(ctx, c.Title, notes, due, s.cardURL(c))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Tasks: " + err.Error()})
		return
	}
	c.TaskRef = strconv.FormatInt(t.ID, 10)
	_ = s.store.UpdateCard(ctx, actorID(ctx), c)
	s.changed(ctx, b, c, "sent_to_tasks", map[string]any{"task_id": t.ID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "task_id": t.ID, "url": conn.public})
}

// ---- Pocket Time ----

func (s *Server) handleCardTimer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), false, false)
	if c == nil {
		return
	}
	var in struct {
		Switch bool  `json:"switch"`
		Stop   bool  `json:"stop"`
		OnlyIf int64 `json:"only_if"`
	}
	_ = decode(r, &in)
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appTime, scopeTimeWrite)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if in.Stop {
		st, err := conn.client.StopTimer(ctx, in.OnlyIf)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Time: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": st.Running, "stopped": st.Stopped, "url": conn.public})
		return
	}
	body := map[string]any{"app_ref": fmt.Sprintf("card:%d", c.ID), "app_label": c.Title, "app_url": s.cardPath(c), "project": b.Title, "deliver": true, "switch": in.Switch}
	if conn.via == modeDirect {
		body["app"], body["source"] = "boards", "boards"
	}
	st, err := conn.client.StartTimer(ctx, body)
	var se *pocket.StatusError
	if errors.As(err, &se) && se.Status == http.StatusConflict {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "conflict": true, "running": st.Running, "error": "Another timer is running in Pocket Time", "url": conn.public})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Time: " + err.Error()})
		return
	}
	s.changed(ctx, b, c, "timer_started", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": st.Running, "stopped": st.Stopped, "url": conn.public})
}

// handleTimer reads the running Pocket Time timer (for the card page).
func (s *Server) handleTimer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conn, err := optional(s.clients(ctx, actorID(ctx)).conn(ctx, appTime, scopeTimeRead))
	if err != nil || conn == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "running": nil})
		return
	}
	st, err := conn.client.Timer(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "running": nil, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "running": st.Running, "url": conn.public})
}

// ---- the worklog contract (Pocket Time writes time spent on cards) ----

func cardRef(ref string) int64 {
	if !strings.HasPrefix(ref, "card:") {
		return 0
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(ref, "card:"), 10, 64)
	return id
}

func (s *Server) handleWorklogList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from, to := dateRange(r, 31)
	wl, err := s.store.WorklogRange(ctx, actorID(ctx), from, to)
	if err != nil {
		s.fail(w, err, "worklog")
		return
	}
	out := []map[string]any{}
	for _, e := range wl {
		c, err := s.store.CardByID(ctx, e.CardID)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"id": strconv.FormatInt(e.ID, 10), "title": firstNonEmpty(e.Title, c.Title), "started_at": e.StartedAt, "ended_at": e.EndedAt,
			"date": e.Date, "duration_s": e.DurationS, "ref": fmt.Sprintf("card:%d", c.ID), "label": c.Title, "url": s.cardPath(c), "source": e.Source, "external_id": e.ExternalID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (s *Server) handleWorklogItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	cards, err := s.store.FindCards(ctx, uid, store.CardFilter{Q: r.URL.Query().Get("q"), Limit: 200})
	if err != nil {
		s.fail(w, err, "worklog")
		return
	}
	// Mine first, then by due date (FindCards' order).
	var mine, rest []store.Card
	for _, c := range cards {
		if containsID(c.Members, uid) {
			mine = append(mine, c)
		} else {
			rest = append(rest, c)
		}
	}
	out := []map[string]any{}
	for _, c := range append(mine, rest...) {
		if len(out) >= 50 {
			break
		}
		out = append(out, map[string]any{"ref": fmt.Sprintf("card:%d", c.ID), "label": c.Title, "detail": c.BoardTitle + " · " + c.ListTitle, "url": s.cardPath(&c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleWorklogSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Ref        string `json:"ref"`
		Title      string `json:"title"`
		Note       string `json:"note"`
		StartedAt  string `json:"started_at"`
		EndedAt    string `json:"ended_at"`
		Date       string `json:"date"`
		DurationS  int    `json:"duration_s"`
		ExternalID string `json:"external_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := cardRef(in.Ref)
	if id == 0 {
		writeErr(w, http.StatusBadRequest, "ref is a card (card:<id>)")
		return
	}
	c, b := s.cardFor(w, r, id, false, false)
	if c == nil {
		return
	}
	if in.DurationS < 0 || in.DurationS > 7*24*3600 {
		writeErr(w, http.StatusBadRequest, "duration_s is 0 to a week")
		return
	}
	if in.Date == "" {
		if t, ok := parseTime(in.StartedAt); ok {
			in.Date = t.Format("2006-01-02")
		} else {
			in.Date = store.Today()
		}
	}
	if !validDate(in.Date) {
		writeErr(w, http.StatusBadRequest, "date is YYYY-MM-DD")
		return
	}
	e := store.Worklog{UserID: actorID(ctx), CardID: c.ID, Title: in.Title, Note: in.Note, StartedAt: in.StartedAt, EndedAt: in.EndedAt, Date: in.Date,
		DurationS: in.DurationS, Source: firstNonEmpty(linkApp(ctx), "app"), ExternalID: in.ExternalID}
	if e.ExternalID != "" {
		if old, err := s.store.DB.QueryContext(ctx, `SELECT source FROM worklog WHERE user_id = ? AND external_id = ?`, e.UserID, e.ExternalID); err == nil {
			var src string
			if old.Next() {
				_ = old.Scan(&src)
			}
			old.Close()
			if src != "" && linkApp(ctx) != "" && src != linkApp(ctx) {
				writeErr(w, http.StatusForbidden, "that time was logged by another app")
				return
			}
		}
	}
	if err := s.store.SaveWorklog(ctx, &e); err != nil {
		s.fail(w, err, "worklog")
		return
	}
	s.changed(ctx, b, c, "time_logged", map[string]any{"duration_s": e.DurationS})
	writeJSON(w, http.StatusOK, map[string]any{"id": strconv.FormatInt(e.ID, 10), "url": s.cardPath(c)})
}

func (s *Server) handleWorklogDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	e, err := s.store.WorklogByID(ctx, actorID(ctx), id)
	if err != nil {
		s.fail(w, err, "worklog")
		return
	}
	if a := linkApp(ctx); a != "" && e.Source != a {
		writeErr(w, http.StatusForbidden, "that time was logged by another app")
		return
	}
	if err := s.store.DeleteWorklog(ctx, actorID(ctx), id); err != nil {
		s.fail(w, err, "worklog")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- for other apps: calendar overlay and a day ----

func (s *Server) handleOverlay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from, to := dateRange(r, 31)
	f, _ := time.ParseInLocation("2006-01-02", from, time.Local)
	t, _ := time.ParseInLocation("2006-01-02", to, time.Local)
	cards, err := s.store.FindCards(ctx, actorID(ctx), store.CardFilter{DueFrom: f.UTC().Format(time.RFC3339), DueTo: t.AddDate(0, 0, 1).UTC().Format(time.RFC3339), IncludeDone: true, Limit: 1000})
	if err != nil {
		s.fail(w, err, "overlay")
		return
	}
	items := []map[string]any{}
	for _, c := range cards {
		due, _ := parseTime(c.DueAt)
		items = append(items, map[string]any{"id": c.ID, "title": c.Title, "date": due.Format("2006-01-02"), "due_at": c.DueAt, "done": c.Done,
			"board": c.BoardTitle, "list": c.ListTitle, "color": c.BoardColor, "url": s.cardURL(&c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	date := chi.URLParam(r, "date")
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "date is YYYY-MM-DD")
		return
	}
	from, to := d.UTC().Format(time.RFC3339), d.AddDate(0, 0, 1).UTC().Format(time.RFC3339)
	uid := actorID(ctx)
	due, err := s.store.FindCards(ctx, uid, store.CardFilter{DueFrom: from, DueTo: to, IncludeDone: true, Limit: 200})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	all, err := s.store.FindCards(ctx, uid, store.CardFilter{IncludeDone: true, Limit: 1000})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	done, created := []store.Card{}, []store.Card{}
	for _, c := range all {
		if t, ok := parseTime(c.DoneAt); ok && t.In(time.Local).Format("2006-01-02") == date {
			done = append(done, c)
		}
		if t, ok := parseTime(c.CreatedAt); ok && t.In(time.Local).Format("2006-01-02") == date && c.CreatedBy != nil && *c.CreatedBy == uid {
			created = append(created, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "due": due, "done": done, "created": created})
}

// ---- the calendar feed ----

// handleICS serves the person's due cards as an iCalendar feed. The token in
// the address is the only key (calendar apps can't log in).
func (s *Server) handleICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok := r.URL.Query().Get("token")
	if len(tok) < 20 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	uid, err := s.store.UserBySetting(ctx, "ics_token", tok)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	now := time.Now()
	cards, err := s.store.FindCards(ctx, uid, store.CardFilter{HasDue: true, DueFrom: now.AddDate(0, -3, 0).UTC().Format(time.RFC3339), IncludeDone: true, Limit: 1000})
	if err != nil {
		s.fail(w, err, "calendar")
		return
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(foldICS(s)); b.WriteString("\r\n") }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Pocket Boards//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Pocket Boards")
	line("X-PUBLISHED-TTL:PT15M")
	stamp := now.UTC().Format("20060102T150405Z")
	for _, c := range cards {
		if len(c.Members) > 0 && !containsID(c.Members, uid) {
			continue
		}
		due, ok := parseTime(c.DueAt)
		if !ok {
			continue
		}
		start := due.Add(-30 * time.Minute)
		if st, ok := parseTime(c.StartAt); ok && st.Before(due) && due.Sub(st) < 24*time.Hour {
			start = st
		}
		title := c.Title
		if c.Done {
			title = "✓ " + title
		}
		line("BEGIN:VEVENT")
		line(fmt.Sprintf("UID:pocket-boards-card-%d@%s", c.ID, hostOf(s.cfg.BaseURL)))
		line("DTSTAMP:" + stamp)
		line("DTSTART:" + start.UTC().Format("20060102T150405Z"))
		line("DTEND:" + due.UTC().Format("20060102T150405Z"))
		line("SUMMARY:" + escICS(title))
		line("DESCRIPTION:" + escICS(fmt.Sprintf("%s · %s\n%s", c.BoardTitle, c.ListTitle, s.cardURL(&c))))
		line("URL:" + s.cardURL(&c))
		line("CATEGORIES:" + escICS(c.BoardTitle))
		if c.Done {
			line("STATUS:CONFIRMED")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="pocket-boards.ics"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write([]byte(b.String()))
}

func escICS(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace(s)
}

// foldICS folds lines longer than 75 bytes (RFC 5545), never inside a UTF-8 rune.
func foldICS(s string) string {
	if len(s) <= 75 {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		l := len(string(r))
		if n+l > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += l
	}
	return b.String()
}

func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return "pocket-boards"
}

func (s *Server) icsURL(st store.Settings) string {
	if st.ICSToken == "" {
		return ""
	}
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/api/v1/calendar.ics?token=" + st.ICSToken
}

func (s *Server) handleNewICSToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	st.ICSToken = store.RandomToken(24)
	if err := s.store.SaveUserSettings(ctx, uid, st); err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ics_url": s.icsURL(st)})
}
