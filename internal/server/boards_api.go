package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- access ----
//
// Every handler that starts from an id goes through one of these: a board
// the caller cannot see is 404 (whatever it holds), a viewer asking to change
// something is 403.

func (s *Server) boardFor(w http.ResponseWriter, r *http.Request, id int64, edit bool) *store.Board {
	b, err := s.store.BoardFor(r.Context(), actorID(r.Context()), id)
	if err != nil {
		s.fail(w, err, "board")
		return nil
	}
	if edit && !b.CanEdit() {
		writeErr(w, http.StatusForbidden, "you can only look at this board")
		return nil
	}
	return b
}

func (s *Server) listFor(w http.ResponseWriter, r *http.Request, id int64, edit bool) (*store.List, *store.Board) {
	l, err := s.store.ListByID(r.Context(), id)
	if err != nil || l.DeletedAt != "" {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, nil
	}
	b := s.boardFor(w, r, l.BoardID, edit)
	if b == nil {
		return nil, nil
	}
	return l, b
}

// cardFor loads a card the caller can see; deleted cards only when trashed is true.
func (s *Server) cardFor(w http.ResponseWriter, r *http.Request, id int64, edit, trashed bool) (*store.Card, *store.Board) {
	c, err := s.store.CardByID(r.Context(), id)
	if err != nil || (c.DeletedAt != "") != trashed {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, nil
	}
	b := s.boardFor(w, r, c.BoardID, edit)
	if b == nil {
		return nil, nil
	}
	return c, b
}

// via says where a change came from: a Pocket app over a link, the Deck API, or here.
func via(ctx context.Context) string {
	if a := linkApp(ctx); a != "" {
		return a
	}
	if v, _ := ctx.Value(deckKey).(bool); v {
		return "deck"
	}
	return ""
}

// changed records what happened on a board and tells everyone watching it.
func (s *Server) changed(ctx context.Context, b *store.Board, c *store.Card, kind string, detail map[string]any) {
	uid := actorID(ctx)
	var cid *int64
	var cardID int64
	if c != nil {
		cid, cardID = &c.ID, c.ID
		if detail == nil {
			detail = map[string]any{}
		}
		if _, ok := detail["title"]; !ok {
			detail["title"] = c.Title
		}
	}
	if kind != "" {
		s.store.AddActivity(ctx, b.ID, cid, uid, kind, detail, via(ctx))
	}
	s.hub.send(liveEvent{BoardID: b.ID, By: uid, Kind: kind, CardID: cardID})
}

func (s *Server) removeFiles(stored []string) {
	for _, f := range stored {
		if f == "" || strings.ContainsAny(f, `/\`) {
			continue
		}
		_ = os.Remove(filepath.Join(s.cfg.FilesDir, f))
	}
}

// ---- home, people, search ----

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	f := false
	boards, err := s.store.ListBoards(ctx, uid, store.BoardFilter{Archived: &f, Counts: true})
	if err != nil {
		s.fail(w, err, "home")
		return
	}
	starred, recent := []int64{}, []int64{}
	byOpen := append([]store.Board(nil), boards...)
	sort.SliceStable(byOpen, func(i, j int) bool { return byOpen[i].OpenedAt > byOpen[j].OpenedAt })
	for _, b := range boards {
		if b.Starred {
			starred = append(starred, b.ID)
		}
	}
	for _, b := range byOpen {
		if b.OpenedAt != "" && len(recent) < 6 {
			recent = append(recent, b.ID)
		}
	}
	now := time.Now()
	week := endOfDay(now).AddDate(0, 0, 7).UTC().Format(time.RFC3339)
	due, err := s.store.FindCards(ctx, uid, store.CardFilter{HasDue: true, DueTo: week, Limit: 200})
	if err != nil {
		s.fail(w, err, "home")
		return
	}
	// Mine, or nobody's on a board I can change.
	mine := due[:0]
	for _, c := range due {
		if containsID(c.Members, uid) || len(c.Members) == 0 {
			mine = append(mine, c)
		}
	}
	counts := map[string]int{}
	tod := endOfDay(now)
	for _, c := range mine {
		t, ok := parseTime(c.DueAt)
		if !ok {
			continue
		}
		switch {
		case t.Before(now):
			counts["overdue"]++
		case t.Before(tod):
			counts["today"]++
		default:
			counts["week"]++
		}
	}
	assigned, _ := s.store.FindCards(ctx, uid, store.CardFilter{Assigned: true, Limit: 1000})
	counts["assigned"] = len(assigned)
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards, "starred": starred, "recent": recent, "due": mine, "counts": counts})
}

// handlePeople lists the people on this box the caller could share a board with.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	out := []map[string]any{}
	for _, u := range users {
		out = append(out, map[string]any{"id": u.ID, "name": u.Name, "email": u.Email})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"boards": []any{}, "cards": []any{}})
		return
	}
	boards, err := s.store.ListBoards(ctx, actorID(ctx), store.BoardFilter{})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	lq := strings.ToLower(q)
	hits := []store.Board{}
	for _, b := range boards {
		if strings.Contains(strings.ToLower(b.Title), lq) || strings.Contains(strings.ToLower(b.Description), lq) {
			hits = append(hits, b)
		}
	}
	cards, err := s.store.FindCards(ctx, actorID(ctx), store.CardFilter{Q: q, IncludeDone: true, Limit: 50})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": hits, "cards": cards})
}

// handleCards is "My cards": cards across every board, by due date.
func (s *Server) handleCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qv := r.URL.Query()
	f := store.CardFilter{Assigned: qv.Get("assigned") != "0", Q: qv.Get("q"), IncludeDone: queryBool(r, "done"), Limit: queryInt(r, "limit")}
	if b := queryInt(r, "board"); b > 0 {
		f.BoardID = int64(b)
	}
	now := time.Now()
	tod := endOfDay(now)
	switch qv.Get("due") {
	case "overdue":
		f.DueTo = now.UTC().Format(time.RFC3339)
	case "today":
		f.DueFrom, f.DueTo = startOfDay(now).UTC().Format(time.RFC3339), tod.UTC().Format(time.RFC3339)
	case "week":
		f.DueFrom, f.DueTo = startOfDay(now).UTC().Format(time.RFC3339), tod.AddDate(0, 0, 7).UTC().Format(time.RFC3339)
	case "later":
		f.DueFrom = tod.AddDate(0, 0, 7).UTC().Format(time.RFC3339)
	}
	cards, err := s.store.FindCards(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "cards")
		return
	}
	if qv.Get("due") == "none" {
		out := cards[:0]
		for _, c := range cards {
			if c.DueAt == "" {
				out = append(out, c)
			}
		}
		cards = out
	}
	writeJSON(w, http.StatusOK, cards)
}

// ---- boards ----

func (s *Server) handleListBoards(w http.ResponseWriter, r *http.Request) {
	f := store.BoardFilter{Counts: true}
	switch r.URL.Query().Get("archived") {
	case "0", "false":
		v := false
		f.Archived = &v
	case "1", "true":
		v := true
		f.Archived = &v
	}
	boards, err := s.store.ListBoards(r.Context(), actorID(r.Context()), f)
	if err != nil {
		s.fail(w, err, "boards")
		return
	}
	writeJSON(w, http.StatusOK, boards)
}

var hexColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func cleanColor(c string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c), "#"))
}

type boardInput struct {
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Color        string    `json:"color"`
	Emoji        string    `json:"emoji"`
	Archived     bool      `json:"archived"`
	Starred      bool      `json:"starred"`
	Position     int       `json:"position"`
	Template     string    `json:"template"`
	TemplateData *template `json:"template_data"`
}

func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var in boardInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	tpl := defaultTemplate()
	if in.Template != "" {
		t, ok := templateByKey(in.Template)
		if !ok {
			writeErr(w, http.StatusBadRequest, "unknown template")
			return
		}
		tpl = t
	} else if in.TemplateData != nil {
		tpl = *in.TemplateData
	}
	b := store.Board{Title: strings.TrimSpace(in.Title), Description: in.Description, Color: cleanColor(in.Color), Emoji: in.Emoji}
	if b.Title == "" {
		b.Title = tpl.Title
	}
	if b.Title == "" {
		writeErr(w, http.StatusBadRequest, "a board needs a name")
		return
	}
	if b.Color == "" {
		b.Color = cleanColor(tpl.Color)
	}
	if b.Color != "" && !hexColor.MatchString(b.Color) {
		writeErr(w, http.StatusBadRequest, "color is a hex colour like 0c66e4")
		return
	}
	if b.Emoji == "" {
		b.Emoji = tpl.Emoji
	}
	if src := linkApp(ctx); src != "" {
		b.Source = src
	}
	if err := s.store.CreateBoard(ctx, actorID(ctx), &b); err != nil {
		s.fail(w, err, "create board")
		return
	}
	if err := s.applyTemplate(ctx, &b, tpl); err != nil {
		s.fail(w, err, "create board")
		return
	}
	s.store.AddActivity(ctx, b.ID, nil, actorID(ctx), "board_created", map[string]any{"title": b.Title}, via(ctx))
	nb, _ := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	writeJSON(w, http.StatusCreated, nb)
}

// applyTemplate adds a template's labels, lists and cards to a new board.
func (s *Server) applyTemplate(ctx context.Context, b *store.Board, t template) error {
	uid := actorID(ctx)
	for _, l := range t.Labels {
		lb := store.Label{BoardID: b.ID, Title: l.Title, Color: cleanColor(l.Color)}
		if !hexColor.MatchString(lb.Color) {
			lb.Color = "8590a2"
		}
		if err := s.store.CreateLabel(ctx, &lb); err != nil {
			return err
		}
	}
	for i, tl := range t.Lists {
		if i >= 30 {
			break
		}
		l := store.List{BoardID: b.ID, Title: strings.TrimSpace(tl.Title), IsDone: tl.IsDone}
		if l.Title == "" {
			continue
		}
		if err := s.store.CreateList(ctx, &l, -1); err != nil {
			return err
		}
		for j, title := range tl.Cards {
			if j >= 100 || strings.TrimSpace(title) == "" {
				continue
			}
			c := store.Card{BoardID: b.ID, ListID: l.ID, Title: strings.TrimSpace(title), Source: b.Source}
			if tl.IsDone {
				c.DoneAt = store.Now()
			}
			if err := s.store.CreateCard(ctx, uid, &c, -1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	full, err := s.boardDoc(ctx, b, queryBool(r, "archived"))
	if err != nil {
		s.fail(w, err, "board")
		return
	}
	if linkApp(ctx) == "" && r.Header.Get("X-Pocket-Peek") == "" {
		_ = s.store.SetBoardPref(ctx, actorID(ctx), b.ID, nil, nil, true)
	}
	writeJSON(w, http.StatusOK, full)
}

// boardDoc is a whole board: lists with their cards, labels and members.
func (s *Server) boardDoc(ctx context.Context, b *store.Board, archived bool) (map[string]any, error) {
	lists, err := s.store.Lists(ctx, b.ID, false)
	if err != nil {
		return nil, err
	}
	cards, err := s.store.BoardCards(ctx, b.ID, archived)
	if err != nil {
		return nil, err
	}
	byList := map[int64][]store.Card{}
	for _, c := range cards {
		byList[c.ListID] = append(byList[c.ListID], c)
	}
	for i := range lists {
		lists[i].Cards = byList[lists[i].ID]
		if lists[i].Cards == nil {
			lists[i].Cards = []store.Card{}
		}
	}
	labels, err := s.store.Labels(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	members, err := s.store.Members(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"board": b, "lists": lists, "labels": labels, "members": members}, nil
}

func (s *Server) handleUpdateBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	var in boardInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	shared := keys["title"] || keys["description"] || keys["color"] || keys["emoji"] || keys["archived"]
	if shared {
		if !b.CanEdit() {
			writeErr(w, http.StatusForbidden, "you can only look at this board")
			return
		}
		if keys["title"] {
			if t := strings.TrimSpace(in.Title); t != "" {
				b.Title = t
			}
		}
		if keys["description"] {
			b.Description = in.Description
		}
		if keys["color"] {
			c := cleanColor(in.Color)
			if !hexColor.MatchString(c) {
				writeErr(w, http.StatusBadRequest, "color is a hex colour like 0c66e4")
				return
			}
			b.Color = c
		}
		if keys["emoji"] {
			b.Emoji = strings.TrimSpace(in.Emoji)
		}
		kind := "board_updated"
		if keys["archived"] && in.Archived != b.Archived {
			b.Archived = in.Archived
			kind = map[bool]string{true: "board_archived", false: "board_unarchived"}[in.Archived]
		}
		if err := s.store.UpdateBoard(ctx, b); err != nil {
			s.fail(w, err, "update board")
			return
		}
		s.changed(ctx, b, nil, kind, map[string]any{"title": b.Title})
	}
	var star *bool
	var pos *int
	if keys["starred"] {
		star = &in.Starred
	}
	if keys["position"] {
		pos = &in.Position
	}
	if star != nil || pos != nil {
		if err := s.store.SetBoardPref(ctx, actorID(ctx), b.ID, star, pos, false); err != nil {
			s.fail(w, err, "update board")
			return
		}
	}
	nb, err := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	if err != nil {
		s.fail(w, err, "board")
		return
	}
	writeJSON(w, http.StatusOK, nb)
}

func (s *Server) handleDeleteBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	b, err := s.store.BoardForAny(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "board")
		return
	}
	if b.Role != store.RoleOwner {
		// A member deleting a shared board leaves it.
		if err := s.store.RemoveMember(ctx, b.ID, uid); err != nil {
			s.fail(w, err, "leave board")
			return
		}
		s.changed(ctx, b, nil, "member_left", map[string]any{"user_id": uid})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "left": true})
		return
	}
	if queryBool(r, "purge") {
		files, err := s.store.PurgeBoard(ctx, b.ID)
		if err != nil {
			s.fail(w, err, "delete board")
			return
		}
		s.removeFiles(files)
		s.hub.send(liveEvent{BoardID: b.ID, By: uid, Kind: "board_deleted"})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "purged": true})
		return
	}
	if err := s.store.TrashBoard(ctx, b.ID, true); err != nil {
		s.fail(w, err, "delete board")
		return
	}
	s.store.AddActivity(ctx, b.ID, nil, uid, "board_deleted", map[string]any{"title": b.Title}, via(ctx))
	s.hub.send(liveEvent{BoardID: b.ID, By: uid, Kind: "board_deleted"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRestoreBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.store.BoardForAny(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "board")
		return
	}
	if b.Role != store.RoleOwner {
		writeErr(w, http.StatusForbidden, "only the owner can restore a board")
		return
	}
	if err := s.store.TrashBoard(ctx, b.ID, false); err != nil {
		s.fail(w, err, "restore board")
		return
	}
	s.changed(ctx, b, nil, "board_restored", map[string]any{"title": b.Title})
	nb, _ := s.store.BoardFor(ctx, actorID(ctx), b.ID)
	writeJSON(w, http.StatusOK, nb)
}

// handleCopyBoard makes a copy the caller owns: labels and lists always,
// cards (with their labels, checklists and dates; not comments or files) when asked.
func (s *Server) handleCopyBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	var in struct {
		Title string `json:"title"`
		Cards bool   `json:"cards"`
	}
	_ = decode(r, &in)
	nb := store.Board{Title: firstNonEmpty(strings.TrimSpace(in.Title), b.Title+" (copy)"), Description: b.Description, Color: b.Color, Emoji: b.Emoji}
	uid := actorID(ctx)
	if err := s.store.CreateBoard(ctx, uid, &nb); err != nil {
		s.fail(w, err, "copy board")
		return
	}
	labels, _ := s.store.Labels(ctx, b.ID)
	labelMap := map[int64]int64{}
	for _, l := range labels {
		nl := store.Label{BoardID: nb.ID, Title: l.Title, Color: l.Color}
		if err := s.store.CreateLabel(ctx, &nl); err != nil {
			s.fail(w, err, "copy board")
			return
		}
		labelMap[l.ID] = nl.ID
	}
	lists, _ := s.store.Lists(ctx, b.ID, false)
	for _, l := range lists {
		nl := store.List{BoardID: nb.ID, Title: l.Title, WIPLimit: l.WIPLimit, IsDone: l.IsDone, Color: l.Color}
		if err := s.store.CreateList(ctx, &nl, -1); err != nil {
			s.fail(w, err, "copy board")
			return
		}
		if !in.Cards {
			continue
		}
		cards, _ := s.store.ListCards(ctx, l.ID, false)
		for _, c := range cards {
			nc := store.Card{BoardID: nb.ID, ListID: nl.ID, Title: c.Title, Description: c.Description, StartAt: c.StartAt, DueAt: c.DueAt, DoneAt: c.DoneAt, CoverColor: c.CoverColor, Repeat: c.Repeat}
			for _, x := range c.Labels {
				if id, ok := labelMap[x]; ok {
					nc.Labels = append(nc.Labels, id)
				}
			}
			if err := s.store.CreateCard(ctx, uid, &nc, -1); err != nil {
				s.fail(w, err, "copy board")
				return
			}
		}
	}
	s.store.AddActivity(ctx, nb.ID, nil, uid, "board_created", map[string]any{"title": nb.Title, "copied_from": b.Title}, via(ctx))
	out, _ := s.store.BoardFor(ctx, uid, nb.ID)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleBoardActivity(w http.ResponseWriter, r *http.Request) {
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	acts, err := s.store.ListActivity(r.Context(), b.ID, int64(queryInt(r, "card")), int64(queryInt(r, "before")), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "activity")
		return
	}
	writeJSON(w, http.StatusOK, acts)
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	all, err := s.store.ListBoards(ctx, uid, store.BoardFilter{Deleted: true})
	if err != nil {
		s.fail(w, err, "bin")
		return
	}
	boards := []store.Board{}
	for _, b := range all {
		if b.Role == store.RoleOwner {
			boards = append(boards, b)
		}
	}
	live, err := s.store.ListBoards(ctx, uid, store.BoardFilter{})
	if err != nil {
		s.fail(w, err, "bin")
		return
	}
	var ids []int64
	titles := map[int64]store.Board{}
	for _, b := range live {
		if b.CanEdit() {
			ids = append(ids, b.ID)
			titles[b.ID] = b
		}
	}
	cards, err := s.store.TrashedCards(ctx, ids)
	if err != nil {
		s.fail(w, err, "bin")
		return
	}
	for i := range cards {
		cards[i].BoardTitle, cards[i].BoardColor = titles[cards[i].BoardID].Title, titles[cards[i].BoardID].Color
	}
	type trashedList struct {
		store.List
		BoardTitle string `json:"board_title"`
		BoardColor string `json:"board_color"`
		CardCount  int    `json:"card_count"`
	}
	lists := []trashedList{}
	for _, id := range ids {
		ls, err := s.store.Lists(ctx, id, true)
		if err != nil {
			s.fail(w, err, "bin")
			return
		}
		for _, l := range ls {
			cs, _ := s.store.ListCards(ctx, l.ID, false)
			lists = append(lists, trashedList{List: l, BoardTitle: titles[id].Title, BoardColor: titles[id].Color, CardCount: len(cs)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards, "lists": lists, "cards": cards})
}

// ---- members ----

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	ms, err := s.store.Members(r.Context(), b.ID)
	if err != nil {
		s.fail(w, err, "members")
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func validRole(r string) bool { return r == store.RoleEditor || r == store.RoleViewer }

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	if b.Role != store.RoleOwner {
		writeErr(w, http.StatusForbidden, "only the owner can share this board")
		return
	}
	var in struct {
		UserID int64  `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Role == "" {
		in.Role = store.RoleEditor
	}
	if !validRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "role is editor or viewer")
		return
	}
	var u *store.User
	var err error
	if in.UserID > 0 {
		u, err = s.store.GetUser(ctx, in.UserID)
	} else {
		u, err = s.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email)))
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "there is nobody with that address on this box")
		return
	}
	if u.ID == b.OwnerID {
		writeErr(w, http.StatusBadRequest, "that is the board's owner")
		return
	}
	if err := s.store.SetMember(ctx, b.ID, u.ID, in.Role); err != nil {
		s.fail(w, err, "share board")
		return
	}
	s.changed(ctx, b, nil, "member_added", map[string]any{"user_id": u.ID, "name": firstNonEmpty(u.Name, u.Email), "role": in.Role})
	go s.notifyShared(u.ID, b, userFrom(ctx))
	ms, _ := s.store.Members(ctx, b.ID)
	writeJSON(w, http.StatusOK, ms)
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	if b.Role != store.RoleOwner {
		writeErr(w, http.StatusForbidden, "only the owner can change sharing")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil || !validRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "role is editor or viewer")
		return
	}
	uid := idParam(r, "uid")
	ms, _ := s.store.Members(ctx, b.ID)
	found := false
	for _, m := range ms {
		found = found || (m.UserID == uid && m.Role != store.RoleOwner)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.SetMember(ctx, b.ID, uid, in.Role); err != nil {
		s.fail(w, err, "share board")
		return
	}
	s.changed(ctx, b, nil, "member_role", map[string]any{"user_id": uid, "role": in.Role})
	ms, _ = s.store.Members(ctx, b.ID)
	writeJSON(w, http.StatusOK, ms)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	uid := idParam(r, "uid")
	if b.Role != store.RoleOwner && uid != actorID(ctx) {
		writeErr(w, http.StatusForbidden, "only the owner can change sharing")
		return
	}
	if uid == b.OwnerID {
		writeErr(w, http.StatusBadRequest, "the owner can't leave their own board")
		return
	}
	if err := s.store.RemoveMember(ctx, b.ID, uid); err != nil {
		s.fail(w, err, "share board")
		return
	}
	s.changed(ctx, b, nil, "member_removed", map[string]any{"user_id": uid})
	ms, _ := s.store.Members(ctx, b.ID)
	writeJSON(w, http.StatusOK, ms)
}

// ---- lists ----

type listInput struct {
	Title    string `json:"title"`
	Position *int   `json:"position"`
	WIPLimit int    `json:"wip_limit"`
	IsDone   bool   `json:"is_done"`
	Color    string `json:"color"`
}

func (s *Server) handleCreateList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), true)
	if b == nil {
		return
	}
	var in listInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	l := store.List{BoardID: b.ID, Title: strings.TrimSpace(in.Title), WIPLimit: max(in.WIPLimit, 0), IsDone: in.IsDone, Color: cleanColor(in.Color)}
	if l.Title == "" {
		writeErr(w, http.StatusBadRequest, "a list needs a name")
		return
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := s.store.CreateList(ctx, &l, pos); err != nil {
		s.fail(w, err, "create list")
		return
	}
	s.changed(ctx, b, nil, "list_created", map[string]any{"list": l.Title})
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) handleUpdateList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.listFor(w, r, idParam(r, "id"), true)
	if l == nil {
		return
	}
	var in listInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] && strings.TrimSpace(in.Title) != "" {
		l.Title = strings.TrimSpace(in.Title)
	}
	if keys["wip_limit"] {
		l.WIPLimit = max(in.WIPLimit, 0)
	}
	if keys["is_done"] {
		l.IsDone = in.IsDone
	}
	if keys["color"] {
		l.Color = cleanColor(in.Color)
	}
	if err := s.store.UpdateList(ctx, l); err != nil {
		s.fail(w, err, "update list")
		return
	}
	if keys["position"] && in.Position != nil {
		if err := s.store.MoveList(ctx, l.ID, *in.Position); err != nil {
			s.fail(w, err, "move list")
			return
		}
	}
	s.changed(ctx, b, nil, "list_updated", map[string]any{"list": l.Title})
	nl, _ := s.store.ListByID(ctx, l.ID)
	writeJSON(w, http.StatusOK, nl)
}

func (s *Server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.listFor(w, r, idParam(r, "id"), true)
	if l == nil {
		return
	}
	if queryBool(r, "purge") {
		cards, _ := s.store.ListCards(ctx, l.ID, false)
		arch, _ := s.store.ListCards(ctx, l.ID, true)
		for _, c := range append(cards, arch...) {
			files, err := s.store.PurgeCard(ctx, &c)
			if err != nil {
				s.fail(w, err, "delete list")
				return
			}
			s.removeFiles(files)
		}
		if _, err := s.store.DB.ExecContext(ctx, `DELETE FROM lists WHERE id = ?`, l.ID); err != nil {
			s.fail(w, err, "delete list")
			return
		}
	} else if err := s.store.TrashList(ctx, l.ID, true); err != nil {
		s.fail(w, err, "delete list")
		return
	}
	s.changed(ctx, b, nil, "list_deleted", map[string]any{"list": l.Title})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRestoreList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, err := s.store.ListByID(ctx, idParam(r, "id"))
	if err != nil || l.DeletedAt == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	b := s.boardFor(w, r, l.BoardID, true)
	if b == nil {
		return
	}
	if err := s.store.TrashList(ctx, l.ID, false); err != nil {
		s.fail(w, err, "restore list")
		return
	}
	if err := s.store.MoveList(ctx, l.ID, -1); err != nil {
		s.fail(w, err, "restore list")
		return
	}
	s.changed(ctx, b, nil, "list_restored", map[string]any{"list": l.Title})
	nl, _ := s.store.ListByID(ctx, l.ID)
	writeJSON(w, http.StatusOK, nl)
}

func (s *Server) handleArchiveListCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.listFor(w, r, idParam(r, "id"), true)
	if l == nil {
		return
	}
	cards, err := s.store.ListCards(ctx, l.ID, false)
	if err != nil {
		s.fail(w, err, "archive")
		return
	}
	uid := actorID(ctx)
	for _, c := range cards {
		c.Archived = true
		if err := s.store.UpdateCard(ctx, uid, &c); err != nil {
			s.fail(w, err, "archive")
			return
		}
	}
	s.changed(ctx, b, nil, "list_archived_cards", map[string]any{"list": l.Title, "count": len(cards)})
	writeJSON(w, http.StatusOK, map[string]any{"archived": len(cards)})
}

func (s *Server) handleSortList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.listFor(w, r, idParam(r, "id"), true)
	if l == nil {
		return
	}
	var in struct {
		By string `json:"by"`
	}
	_ = decode(r, &in)
	cards, err := s.store.ListCards(ctx, l.ID, false)
	if err != nil {
		s.fail(w, err, "sort")
		return
	}
	switch in.By {
	case "due":
		sort.SliceStable(cards, func(i, j int) bool {
			a, c := cards[i].DueAt, cards[j].DueAt
			if (a == "") != (c == "") {
				return c == ""
			}
			return a < c
		})
	case "title":
		sort.SliceStable(cards, func(i, j int) bool { return strings.ToLower(cards[i].Title) < strings.ToLower(cards[j].Title) })
	case "created":
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].ID < cards[j].ID })
	default:
		writeErr(w, http.StatusBadRequest, "sort by due, title or created")
		return
	}
	for i, c := range cards {
		if _, err := s.store.DB.ExecContext(ctx, `UPDATE cards SET position = ? WHERE id = ?`, i, c.ID); err != nil {
			s.fail(w, err, "sort")
			return
		}
	}
	s.store.TouchBoard(ctx, b.ID)
	s.changed(ctx, b, nil, "list_sorted", map[string]any{"list": l.Title, "by": in.By})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- labels ----

func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b := s.boardFor(w, r, idParam(r, "id"), true)
	if b == nil {
		return
	}
	var in store.Label
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	l := store.Label{BoardID: b.ID, Title: strings.TrimSpace(in.Title), Color: cleanColor(in.Color)}
	if l.Color == "" {
		l.Color = "8590a2"
	}
	if !hexColor.MatchString(l.Color) {
		writeErr(w, http.StatusBadRequest, "color is a hex colour like 4bce97")
		return
	}
	if err := s.store.CreateLabel(ctx, &l); err != nil {
		s.fail(w, err, "create label")
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) labelFor(w http.ResponseWriter, r *http.Request) (*store.Label, *store.Board) {
	l, err := s.store.LabelByID(r.Context(), idParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return nil, nil
	}
	b := s.boardFor(w, r, l.BoardID, true)
	if b == nil {
		return nil, nil
	}
	return l, b
}

func (s *Server) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.labelFor(w, r)
	if l == nil {
		return
	}
	var in store.Label
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] {
		l.Title = strings.TrimSpace(in.Title)
	}
	if keys["color"] {
		c := cleanColor(in.Color)
		if !hexColor.MatchString(c) {
			writeErr(w, http.StatusBadRequest, "color is a hex colour like 4bce97")
			return
		}
		l.Color = c
	}
	if keys["position"] {
		l.Position = in.Position
	}
	if err := s.store.UpdateLabel(ctx, l); err != nil {
		s.fail(w, err, "update label")
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l, b := s.labelFor(w, r)
	if l == nil {
		return
	}
	if err := s.store.DeleteLabel(ctx, l); err != nil {
		s.fail(w, err, "delete label")
		return
	}
	s.changed(ctx, b, nil, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- time helpers ----

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func endOfDay(t time.Time) time.Time { return startOfDay(t).AddDate(0, 0, 1) }

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

var errViewer = errors.New("you can only look at this board")
