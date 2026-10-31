package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- cards ----

type cardInput struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	StartAt     string  `json:"start_at"`
	DueAt       string  `json:"due_at"`
	Done        bool    `json:"done"`
	Archived    bool    `json:"archived"`
	CoverColor  string  `json:"cover_color"`
	Repeat      string  `json:"repeat"`
	ListID      int64   `json:"list_id"`
	BoardID     int64   `json:"board_id"`
	Position    *int    `json:"position"`
	Labels      []int64 `json:"labels"`
	Members     []int64 `json:"members"`
	Quick       bool    `json:"quick"`
	ExternalID  string  `json:"external_id"`
}

var repeats = map[string]bool{"": true, "daily": true, "weekdays": true, "weekly": true, "monthly": true, "yearly": true}

// normTime turns what was sent as a date or time into RFC 3339 ("" stays "").
func normTime(v, defaultTime string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	if len(v) == 10 {
		if defaultTime == "" {
			defaultTime = "17:00"
		}
		v += "T" + defaultTime
	}
	t, ok := parseTime(v)
	if !ok {
		return "", false
	}
	return t.Format(time.RFC3339), true
}

func (s *Server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.listFor(w, r, idParam(r, "id"), true)
	if l == nil {
		return
	}
	var in cardInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, status, msg := s.newCard(ctx, b, l, in)
	if msg != "" {
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// handleCreateCardAnywhere is POST /cards {board_id?, list_id?, title…}: for
// other apps that don't know a list — the first list of the board (or of the
// first board the person can change).
func (s *Server) handleCreateCardAnywhere(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in cardInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(ctx)
	var l *store.List
	var b *store.Board
	if in.ListID > 0 {
		if l, b = s.listFor(w, r, in.ListID, true); l == nil {
			return
		}
	} else {
		var candidates []store.Board
		if in.BoardID > 0 {
			if b = s.boardFor(w, r, in.BoardID, true); b == nil {
				return
			}
			candidates = []store.Board{*b}
		} else {
			f := false
			all, _ := s.store.ListBoards(ctx, uid, store.BoardFilter{Archived: &f})
			for _, x := range all {
				if x.CanEdit() {
					candidates = append(candidates, x)
				}
			}
		}
		for _, cb := range candidates {
			lists, _ := s.store.Lists(ctx, cb.ID, false)
			for _, x := range lists {
				if !x.IsDone {
					b, l = &cb, &x
					break
				}
			}
			if l != nil {
				break
			}
		}
		if l == nil {
			writeErr(w, http.StatusBadRequest, "there is no board with a list to add the card to")
			return
		}
	}
	c, status, msg := s.newCard(ctx, b, l, in)
	if msg != "" {
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) newCard(ctx context.Context, b *store.Board, l *store.List, in cardInput) (*store.Card, int, string) {
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	c := store.Card{BoardID: b.ID, ListID: l.ID, Title: strings.TrimSpace(in.Title), Description: in.Description, CoverColor: cleanColor(in.CoverColor),
		Repeat: in.Repeat, Labels: in.Labels, ExternalID: in.ExternalID}
	if in.Quick {
		labels, _ := s.store.Labels(ctx, b.ID)
		members, _ := s.store.Members(ctx, b.ID)
		q := parseQuick(c.Title, labels, members, uid, st.DueTimeDefault, time.Now())
		c.Title = q.title
		c.Labels = append(c.Labels, q.labels...)
		in.Members = append(in.Members, q.members...)
		if q.due != "" && in.DueAt == "" {
			in.DueAt = q.due
		}
	}
	if c.Title == "" {
		return nil, http.StatusBadRequest, "a card needs a title"
	}
	if !repeats[c.Repeat] {
		return nil, http.StatusBadRequest, "repeat is daily, weekdays, weekly, monthly or yearly"
	}
	var ok bool
	if c.DueAt, ok = normTime(in.DueAt, st.DueTimeDefault); !ok {
		return nil, http.StatusBadRequest, "due_at is a date or date and time"
	}
	if c.StartAt, ok = normTime(in.StartAt, "09:00"); !ok {
		return nil, http.StatusBadRequest, "start_at is a date or date and time"
	}
	can, _ := s.store.MemberIDs(ctx, b.ID)
	for _, m := range in.Members {
		if containsID(can, m) && !containsID(c.Members, m) {
			c.Members = append(c.Members, m)
		}
	}
	if l.IsDone || in.Done {
		c.DoneAt = store.Now()
	}
	if src := linkApp(ctx); src != "" {
		c.Source = src
	} else if via(ctx) == "deck" {
		c.Source = "deck"
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := s.store.CreateCard(ctx, uid, &c, pos); err != nil {
		return nil, http.StatusInternalServerError, "create card: " + err.Error()
	}
	s.changed(ctx, b, &c, "card_created", map[string]any{"list": l.Title})
	s.publishCard(ctx, "boards.card_created", b, l, &c, nil)
	for _, m := range c.Members {
		if m != uid {
			go s.notifyAssigned(m, b, &c, userFrom(ctx))
		}
	}
	return &c, 0, ""
}

func (s *Server) handleGetCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), false, queryBool(r, "trashed"))
	if c == nil {
		return
	}
	doc, err := s.cardDoc(ctx, b, c)
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) cardDoc(ctx context.Context, b *store.Board, c *store.Card) (map[string]any, error) {
	l, err := s.store.ListByID(ctx, c.ListID)
	if err != nil {
		return nil, err
	}
	labels, err := s.store.Labels(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	members, err := s.store.Members(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	comments, err := s.store.Comments(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	atts, err := s.store.Attachments(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	acts, err := s.store.ListActivity(ctx, b.ID, c.ID, 0, 50)
	if err != nil {
		return nil, err
	}
	wl, err := s.store.CardWorklog(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, x := range wl {
		total += x.DurationS
	}
	return map[string]any{"card": c, "board": b, "list": l, "labels": labels, "members": members, "comments": comments,
		"attachments": atts, "activity": acts, "worklog": wl, "time_spent_s": total}, nil
}

func (s *Server) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	var in cardInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	nc, status, msg := s.updateCard(ctx, b, c, in, keys)
	if msg != "" {
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, nc)
}

// updateCard applies a PATCH-style change (also used by the Deck API).
func (s *Server) updateCard(ctx context.Context, b *store.Board, c *store.Card, in cardInput, keys map[string]bool) (*store.Card, int, string) {
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	before := *c
	if keys["title"] {
		t := strings.TrimSpace(in.Title)
		if t == "" {
			return nil, http.StatusBadRequest, "a card needs a title"
		}
		c.Title = t
	}
	if keys["description"] {
		c.Description = in.Description
	}
	var ok bool
	if keys["due_at"] {
		if c.DueAt, ok = normTime(in.DueAt, st.DueTimeDefault); !ok {
			return nil, http.StatusBadRequest, "due_at is a date or date and time"
		}
	}
	if keys["start_at"] {
		if c.StartAt, ok = normTime(in.StartAt, "09:00"); !ok {
			return nil, http.StatusBadRequest, "start_at is a date or date and time"
		}
	}
	if keys["cover_color"] {
		cc := cleanColor(in.CoverColor)
		if cc != "" && !hexColor.MatchString(cc) {
			return nil, http.StatusBadRequest, "cover_color is a hex colour"
		}
		c.CoverColor = cc
	}
	if keys["repeat"] {
		if !repeats[in.Repeat] {
			return nil, http.StatusBadRequest, "repeat is daily, weekdays, weekly, monthly or yearly"
		}
		c.Repeat = in.Repeat
	}
	if keys["archived"] {
		c.Archived = in.Archived
	}
	if keys["done"] {
		if in.Done && c.DoneAt == "" {
			c.DoneAt = store.Now()
		} else if !in.Done {
			c.DoneAt = ""
		}
	}
	// Where it goes.
	target := c.ListID
	if keys["list_id"] && in.ListID > 0 {
		target = in.ListID
	}
	moved := target != c.ListID || keys["position"] || (keys["board_id"] && in.BoardID != c.BoardID && in.BoardID > 0)
	var tl *store.List
	if moved {
		var err error
		tl, err = s.store.ListByID(ctx, target)
		if err != nil || tl.DeletedAt != "" {
			return nil, http.StatusBadRequest, "that list does not exist"
		}
		if tl.BoardID != c.BoardID {
			tb, err := s.store.BoardFor(ctx, uid, tl.BoardID)
			if err != nil || !tb.CanEdit() {
				return nil, http.StatusBadRequest, "you can't add cards to that board"
			}
		}
		if target != c.ListID {
			// Into a done list: done; out of one into an ordinary list: not done.
			ol, _ := s.store.ListByID(ctx, c.ListID)
			if tl.IsDone && c.DoneAt == "" {
				c.DoneAt = store.Now()
			} else if !tl.IsDone && ol != nil && ol.IsDone && !(keys["done"] && in.Done) {
				c.DoneAt = ""
			}
		}
	}
	if err := s.store.UpdateCard(ctx, uid, c); err != nil {
		return nil, http.StatusInternalServerError, "update card: " + err.Error()
	}
	if moved {
		pos := -1
		if in.Position != nil {
			pos = *in.Position
		}
		var err error
		if tl.BoardID != c.BoardID {
			err = s.store.MoveCardToBoard(ctx, c.ID, target, pos)
		} else if before.Archived != c.Archived || target != c.ListID || in.Position != nil {
			err = s.store.MoveCard(ctx, c.ID, target, pos)
		}
		if err != nil {
			return nil, http.StatusInternalServerError, "move card: " + err.Error()
		}
	} else if before.Archived != c.Archived && !c.Archived {
		_ = s.store.MoveCard(ctx, c.ID, c.ListID, -1)
	}
	nc, err := s.store.CardByID(ctx, c.ID)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	nl, _ := s.store.ListByID(ctx, nc.ListID)
	// What happened, for the activity log and the event bus.
	switch {
	case before.BoardID != nc.BoardID:
		s.changed(ctx, b, nc, "card_moved_board", map[string]any{"to_board_id": nc.BoardID})
		if nb, err := s.store.BoardFor(ctx, uid, nc.BoardID); err == nil {
			s.changed(ctx, nb, nc, "card_moved_in", map[string]any{"from": b.Title, "list": nl.Title})
			b = nb
		}
		s.publishCard(ctx, "boards.card_moved", b, nl, nc, map[string]any{"from_list": ""})
	case before.ListID != nc.ListID:
		ol, _ := s.store.ListByID(ctx, before.ListID)
		from := ""
		if ol != nil {
			from = ol.Title
		}
		s.changed(ctx, b, nc, "card_moved", map[string]any{"from": from, "to": nl.Title})
		s.publishCard(ctx, "boards.card_moved", b, nl, nc, map[string]any{"from_list": from})
	case moved:
		s.changed(ctx, b, nc, "", nil) // reordered in its list
	}
	if before.DoneAt == "" && nc.DoneAt != "" {
		s.changed(ctx, b, nc, "card_done", nil)
		s.publishCard(ctx, "boards.card_done", b, nl, nc, nil)
		if nc.Repeat != "" {
			s.repeatCard(ctx, b, nc, before.ListID)
		}
	} else if before.DoneAt != "" && nc.DoneAt == "" {
		s.changed(ctx, b, nc, "card_reopened", nil)
	}
	if before.Archived != nc.Archived {
		s.changed(ctx, b, nc, map[bool]string{true: "card_archived", false: "card_unarchived"}[nc.Archived], nil)
	}
	details := map[string]any{}
	if before.Title != nc.Title {
		details["title_from"] = before.Title
	}
	if before.DueAt != nc.DueAt {
		details["due_at"] = nc.DueAt
	}
	if before.Description != nc.Description {
		details["description"] = true
	}
	if before.StartAt != nc.StartAt || before.Repeat != nc.Repeat || before.CoverColor != nc.CoverColor {
		details["details"] = true
	}
	if len(details) > 0 {
		s.changed(ctx, b, nc, "card_updated", details)
	}
	return nc, 0, ""
}

// repeatCard puts the next one of a repeating card at the top of the list
// it was in before it was done (or the first list that isn't a done list),
// due the next time after the old due date (or after today when it had none).
func (s *Server) repeatCard(ctx context.Context, b *store.Board, c *store.Card, fromList int64) {
	base, ok := parseTime(c.DueAt)
	if !ok {
		base = time.Now()
	}
	next := nextRepeat(base, c.Repeat)
	for !next.After(time.Now()) {
		next = nextRepeat(next, c.Repeat)
	}
	lists, _ := s.store.Lists(ctx, b.ID, false)
	var target *store.List
	for i := range lists {
		if lists[i].ID == fromList && !lists[i].IsDone {
			target = &lists[i]
		}
	}
	for i := range lists {
		if target == nil && !lists[i].IsDone {
			target = &lists[i]
		}
	}
	if target == nil {
		return
	}
	nc := store.Card{BoardID: b.ID, ListID: target.ID, Title: c.Title, Description: uncheck(c.Description), DueAt: next.Format(time.RFC3339),
		CoverColor: c.CoverColor, Repeat: c.Repeat, Labels: c.Labels, Members: c.Members}
	if c.StartAt != "" {
		if st, ok := parseTime(c.StartAt); ok {
			nc.StartAt = st.Add(next.Sub(base)).Format(time.RFC3339)
		}
	}
	if err := s.store.CreateCard(ctx, actorID(ctx), &nc, -1); err != nil {
		s.log.Warn("repeating card", "card", c.ID, "err", err)
		return
	}
	// The repeat moves to the new card, so the done one won't repeat again.
	c.Repeat = ""
	_ = s.store.UpdateCard(ctx, actorID(ctx), c)
	s.changed(ctx, b, &nc, "card_repeated", map[string]any{"from_card": c.ID})
}

func nextRepeat(t time.Time, rep string) time.Time {
	switch rep {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekdays":
		n := t.AddDate(0, 0, 1)
		for n.Weekday() == time.Saturday || n.Weekday() == time.Sunday {
			n = n.AddDate(0, 0, 1)
		}
		return n
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return t.AddDate(0, 1, 0)
	case "yearly":
		return t.AddDate(1, 0, 0)
	}
	return t.AddDate(0, 0, 1)
}

var checkedLine = regexp.MustCompile(`(?m)^(\s*[-*+] )\[[xX]\]`)

// uncheck un-ticks every task line (a repeating card starts fresh).
func uncheck(desc string) string { return checkedLine.ReplaceAllString(desc, "$1[ ]") }

func (s *Server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	purge := queryBool(r, "purge")
	c, err := s.store.CardByID(ctx, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if c.DeletedAt != "" && !purge {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	b := s.boardFor(w, r, c.BoardID, true)
	if b == nil {
		return
	}
	if purge {
		files, err := s.store.PurgeCard(ctx, c)
		if err != nil {
			s.fail(w, err, "delete card")
			return
		}
		s.removeFiles(files)
		s.changed(ctx, b, nil, "card_purged", map[string]any{"title": c.Title})
	} else {
		if err := s.store.TrashCard(ctx, c, true); err != nil {
			s.fail(w, err, "delete card")
			return
		}
		s.changed(ctx, b, c, "card_deleted", nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRestoreCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, true)
	if c == nil {
		return
	}
	if l, err := s.store.ListByID(ctx, c.ListID); err == nil && l.DeletedAt != "" {
		_ = s.store.TrashList(ctx, l.ID, false)
	}
	if err := s.store.TrashCard(ctx, c, false); err != nil {
		s.fail(w, err, "restore card")
		return
	}
	s.changed(ctx, b, c, "card_restored", nil)
	nc, _ := s.store.CardByID(ctx, c.ID)
	writeJSON(w, http.StatusOK, nc)
}

func (s *Server) handleCopyCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	var in struct {
		Title    string `json:"title"`
		ListID   int64  `json:"list_id"`
		Position *int   `json:"position"`
	}
	_ = decode(r, &in)
	listID := c.ListID
	if in.ListID > 0 {
		l, lb := s.listFor(w, r, in.ListID, true)
		if l == nil {
			return
		}
		if lb.ID != b.ID {
			writeErr(w, http.StatusBadRequest, "copy to a list on the same board, then move it")
			return
		}
		listID = l.ID
	}
	nc := store.Card{BoardID: b.ID, ListID: listID, Title: firstNonEmpty(strings.TrimSpace(in.Title), c.Title), Description: c.Description, StartAt: c.StartAt,
		DueAt: c.DueAt, CoverColor: c.CoverColor, Repeat: c.Repeat, Labels: c.Labels, Members: c.Members}
	pos := c.Position + 1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := s.store.CreateCard(ctx, actorID(ctx), &nc, pos); err != nil {
		s.fail(w, err, "copy card")
		return
	}
	s.changed(ctx, b, &nc, "card_created", map[string]any{"copied_from": c.Title})
	writeJSON(w, http.StatusCreated, nc)
}

func (s *Server) handleCardLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	on := r.Method == http.MethodPut
	if err := s.store.SetCardLabel(ctx, c, idParam(r, "label"), on); err != nil {
		s.fail(w, err, "label")
		return
	}
	s.changed(ctx, b, c, "", nil)
	nc, _ := s.store.CardByID(ctx, c.ID)
	writeJSON(w, http.StatusOK, nc)
}

func (s *Server) handleCardMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	uid := idParam(r, "uid")
	on := r.Method == http.MethodPut
	if on {
		can, _ := s.store.MemberIDs(ctx, b.ID)
		if !containsID(can, uid) {
			writeErr(w, http.StatusBadRequest, "share the board with them first")
			return
		}
	}
	added, err := s.store.SetCardMember(ctx, c, uid, on)
	if err != nil {
		s.fail(w, err, "assign")
		return
	}
	if added {
		name := ""
		if u, err := s.store.GetUser(ctx, uid); err == nil {
			name = firstNonEmpty(u.Name, u.Email)
		}
		kind := map[bool]string{true: "card_assigned", false: "card_unassigned"}[on]
		s.changed(ctx, b, c, kind, map[string]any{"user_id": uid, "name": name})
		if on {
			l, _ := s.store.ListByID(ctx, c.ListID)
			s.publishCard(ctx, "boards.card_assigned", b, l, c, map[string]any{"assignee_id": uid, "assignee": name})
			if uid != actorID(ctx) {
				go s.notifyAssigned(uid, b, c, userFrom(ctx))
			}
		}
	}
	nc, _ := s.store.CardByID(ctx, c.ID)
	writeJSON(w, http.StatusOK, nc)
}

var taskLine = regexp.MustCompile(`^(\s*[-*+] \[)([ xX])(\])`)

// handleChecklist ticks the index-th task line of the description.
func (s *Server) handleChecklist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	idx, _ := strconv.Atoi(chi.URLParam(r, "index"))
	var in struct {
		Done bool `json:"done"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	lines := strings.Split(c.Description, "\n")
	n := -1
	found := false
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence || !taskLine.MatchString(line) {
			continue
		}
		n++
		if n == idx {
			mark := " "
			if in.Done {
				mark = "x"
			}
			lines[i] = taskLine.ReplaceAllString(line, "${1}"+mark+"${3}")
			found = true
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such checklist item")
		return
	}
	c.Description = strings.Join(lines, "\n")
	if err := s.store.UpdateCard(ctx, actorID(ctx), c); err != nil {
		s.fail(w, err, "checklist")
		return
	}
	nc, _ := s.store.CardByID(ctx, c.ID)
	kind := ""
	if in.Done && nc.ChecklistTotal > 0 && nc.ChecklistDone == nc.ChecklistTotal {
		kind = "checklist_complete"
	}
	s.changed(ctx, b, nc, kind, nil)
	writeJSON(w, http.StatusOK, nc)
}

// ---- comments ----

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	c, _ := s.cardFor(w, r, idParam(r, "id"), false, false)
	if c == nil {
		return
	}
	cs, err := s.store.Comments(r.Context(), c.ID)
	if err != nil {
		s.fail(w, err, "comments")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Viewers may comment: they're part of the conversation.
	c, b := s.cardFor(w, r, idParam(r, "id"), false, false)
	if c == nil {
		return
	}
	var in struct {
		Message  string `json:"message"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cm, status, msg := s.addComment(ctx, b, c, strings.TrimSpace(in.Message), in.ParentID)
	if msg != "" {
		writeErr(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, cm)
}

func (s *Server) addComment(ctx context.Context, b *store.Board, c *store.Card, message string, parent *int64) (*store.Comment, int, string) {
	if message == "" {
		return nil, http.StatusBadRequest, "write something first"
	}
	if len(message) > 20000 {
		return nil, http.StatusBadRequest, "that comment is too long"
	}
	if parent != nil {
		if p, err := s.store.CommentByID(ctx, *parent); err != nil || p.CardID != c.ID {
			parent = nil
		}
	}
	cm := store.Comment{CardID: c.ID, Message: message, ParentID: parent, Source: firstNonEmpty(linkApp(ctx), map[bool]string{true: "deck", false: "app"}[via(ctx) == "deck"])}
	if err := s.store.CreateComment(ctx, actorID(ctx), &cm); err != nil {
		return nil, http.StatusInternalServerError, "comment: " + err.Error()
	}
	s.store.TouchBoard(ctx, b.ID)
	s.changed(ctx, b, c, "comment", map[string]any{"comment_id": cm.ID, "excerpt": excerpt(message, 120)})
	l, _ := s.store.ListByID(ctx, c.ListID)
	s.publishCard(ctx, "boards.comment_added", b, l, c, map[string]any{"comment_id": cm.ID, "excerpt": excerpt(message, 200)})
	go s.notifyComment(b, c, &cm, userFrom(ctx))
	return &cm, 0, ""
}

func (s *Server) commentFor(w http.ResponseWriter, r *http.Request) (*store.Comment, *store.Card, *store.Board) {
	ctx := r.Context()
	cm, err := s.store.CommentByID(ctx, idParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, nil, nil
	}
	c, b := s.cardFor(w, r, cm.CardID, false, false)
	if c == nil {
		return nil, nil, nil
	}
	if cm.UserID == nil || *cm.UserID != actorID(ctx) {
		writeErr(w, http.StatusForbidden, "only the person who wrote it can change it")
		return nil, nil, nil
	}
	return cm, c, b
}

func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cm, c, b := s.commentFor(w, r)
	if cm == nil {
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Message) == "" {
		writeErr(w, http.StatusBadRequest, "write something first")
		return
	}
	cm.Message = strings.TrimSpace(in.Message)
	if err := s.store.UpdateComment(ctx, cm); err != nil {
		s.fail(w, err, "comment")
		return
	}
	s.changed(ctx, b, c, "", nil)
	ncm, _ := s.store.CommentByID(ctx, cm.ID)
	writeJSON(w, http.StatusOK, ncm)
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cm, c, b := s.commentFor(w, r)
	if cm == nil {
		return
	}
	if err := s.store.DeleteComment(ctx, cm.ID); err != nil {
		s.fail(w, err, "comment")
		return
	}
	s.changed(ctx, b, c, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- attachments ----

func (s *Server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	c, _ := s.cardFor(w, r, idParam(r, "id"), false, false)
	if c == nil {
		return
	}
	as, err := s.store.Attachments(r.Context(), c.ID)
	if err != nil {
		s.fail(w, err, "files")
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, b := s.cardFor(w, r, idParam(r, "id"), true, false)
	if c == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "send the file as multipart form data (field \"file\"), up to "+humanBytes(s.cfg.MaxBodyBytes))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	defer f.Close()
	a, err := s.saveAttachment(ctx, c, hdr.Filename, hdr.Header.Get("Content-Type"), f)
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	s.changed(ctx, b, c, "attachment_added", map[string]any{"filename": a.Filename})
	writeJSON(w, http.StatusCreated, a)
}

// saveAttachment stores the bytes under a random name and records them.
func (s *Server) saveAttachment(ctx context.Context, c *store.Card, filename, mimeType string, rd io.Reader) (*store.Attachment, error) {
	filename = filepath.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "" || filename == "." || filename == "/" {
		filename = "file"
	}
	rnd := make([]byte, 16)
	_, _ = rand.Read(rnd)
	stored := hex.EncodeToString(rnd) + strings.ToLower(filepath.Ext(filename))
	if len(stored) > 60 {
		stored = stored[:32]
	}
	path := filepath.Join(s.cfg.FilesDir, stored)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(out, rd)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
			mimeType = t
		} else {
			mimeType = "application/octet-stream"
		}
	}
	a := store.Attachment{CardID: c.ID, Filename: filename, Mime: mimeType, Size: n, Stored: stored}
	if err := s.store.CreateAttachment(ctx, actorID(ctx), &a); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	s.store.TouchBoard(ctx, c.BoardID)
	return &a, nil
}

func (s *Server) attachmentFor(w http.ResponseWriter, r *http.Request, edit bool) (*store.Attachment, *store.Card, *store.Board) {
	a, err := s.store.AttachmentByID(r.Context(), idParam(r, "id"))
	if err != nil || a.DeletedAt != "" {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, nil, nil
	}
	c, b := s.cardFor(w, r, a.CardID, edit, false)
	if c == nil {
		return nil, nil, nil
	}
	return a, c, b
}

func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request, a *store.Attachment) {
	f, err := os.Open(filepath.Join(s.cfg.FilesDir, a.Stored))
	if err != nil {
		writeErr(w, http.StatusNotFound, "the file is gone")
		return
	}
	defer f.Close()
	disp := "inline"
	if queryBool(r, "download") || !safeInline(a.Mime) {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": a.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	fi, _ := f.Stat()
	mod := time.Time{}
	if fi != nil {
		mod = fi.ModTime()
	}
	http.ServeContent(w, r, a.Filename, mod, f)
}

// safeInline: only types a browser can't run as a page are shown inline.
func safeInline(m string) bool {
	switch {
	case strings.HasPrefix(m, "image/") && m != "image/svg+xml":
		return true
	case strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"), m == "application/pdf", m == "text/plain":
		return true
	}
	return false
}

func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	a, _, _ := s.attachmentFor(w, r, false)
	if a == nil {
		return
	}
	s.serveAttachment(w, r, a)
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, c, b := s.attachmentFor(w, r, true)
	if a == nil {
		return
	}
	if err := s.store.TrashAttachment(ctx, a.ID, true); err != nil {
		s.fail(w, err, "delete file")
		return
	}
	s.store.TouchBoard(ctx, b.ID)
	s.changed(ctx, b, c, "attachment_deleted", map[string]any{"filename": a.Filename})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- quick add ----

type quickResult struct {
	title   string
	labels  []int64
	members []int64
	due     string
}

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// parseQuick reads "#label", "@person" and "!due" out of a card title.
// Unknown #words and @words stay in the title.
func parseQuick(text string, labels []store.Label, members []store.Member, me int64, defTime string, now time.Time) quickResult {
	var q quickResult
	var keep []string
	var day time.Time
	hasDay, clock := false, ""
	for _, w := range strings.Fields(text) {
		lw := strings.ToLower(w)
		switch {
		case len(w) > 1 && w[0] == '#':
			name := strings.ReplaceAll(lw[1:], "_", " ")
			hit := false
			for _, l := range labels {
				if strings.ToLower(l.Title) == name || (len(name) >= 3 && strings.HasPrefix(strings.ToLower(l.Title), name)) {
					q.labels = append(q.labels, l.ID)
					hit = true
					break
				}
			}
			if !hit {
				keep = append(keep, w)
			}
		case len(w) > 1 && w[0] == '@':
			name := lw[1:]
			hit := false
			for _, m := range members {
				first := strings.ToLower(strings.Fields(m.Name + " x")[0])
				local := strings.ToLower(strings.SplitN(m.Email, "@", 2)[0])
				if name == first || name == local || name == strings.ToLower(m.Email) || (name == "me" && m.UserID == me) {
					q.members = append(q.members, m.UserID)
					hit = true
					break
				}
			}
			if !hit {
				keep = append(keep, w)
			}
		case len(w) > 1 && w[0] == '!':
			v := lw[1:]
			switch {
			case v == "today":
				day, hasDay = now, true
			case v == "tomorrow" || v == "tmr":
				day, hasDay = now.AddDate(0, 0, 1), true
			case len(v) >= 3 && weekdayOf(v[:3]) >= 0:
				wd := time.Weekday(weekdayOf(v[:3]))
				d := now.AddDate(0, 0, 1)
				for d.Weekday() != wd {
					d = d.AddDate(0, 0, 1)
				}
				day, hasDay = d, true
			case regexp.MustCompile(`^\d+[dw]$`).MatchString(v):
				n, _ := strconv.Atoi(v[:len(v)-1])
				if v[len(v)-1] == 'w' {
					n *= 7
				}
				day, hasDay = now.AddDate(0, 0, n), true
			case regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(v):
				if t, err := time.ParseInLocation("2006-01-02", v, now.Location()); err == nil {
					day, hasDay = t, true
				} else {
					keep = append(keep, w)
				}
			case regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`).MatchString(v):
				clock = v
			default:
				keep = append(keep, w)
			}
		default:
			keep = append(keep, w)
		}
	}
	q.title = strings.Join(keep, " ")
	if clock != "" && !hasDay {
		day, hasDay = now, true
	}
	if hasDay {
		if clock == "" {
			clock = firstNonEmpty(defTime, "17:00")
		}
		parts := strings.SplitN(clock, ":", 2)
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		y, mo, d := day.Date()
		q.due = time.Date(y, mo, d, h, m, 0, 0, now.Location()).Format(time.RFC3339)
	}
	return q
}

func weekdayOf(s string) int {
	if wd, ok := weekdays[s]; ok {
		return int(wd)
	}
	return -1
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d KB", n>>10)
}
