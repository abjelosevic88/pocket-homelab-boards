package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

type bapp struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	alice string // admin
	bob   string // member
	ids   map[string]int64
}

func newBoardsApp(t *testing.T) *bapp {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.BaseURL = "http://boards.test"
	cfg.MaxBodyBytes = 64 << 20
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &bapp{t: t, st: st, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *bapp) do(auth, method, path string, body any) (int, []byte) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

// want calls and fails unless the status matches; the body is decoded into out (if given).
func (a *bapp) want(status int, auth, method, path string, body, out any) {
	a.t.Helper()
	code, raw := a.do(auth, method, path, body)
	if code != status {
		a.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, status, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			a.t.Fatalf("%s %s: decode: %v: %s", method, path, err, raw)
		}
	}
}

type boardDoc struct {
	Board   store.Board    `json:"board"`
	Lists   []store.List   `json:"lists"`
	Labels  []store.Label  `json:"labels"`
	Members []store.Member `json:"members"`
}

// newBoard makes a board with the default lists (To do, Doing, Done) for auth.
func (a *bapp) newBoard(auth, title string) boardDoc {
	a.t.Helper()
	var b store.Board
	a.want(201, auth, "POST", "/api/v1/boards", map[string]any{"title": title}, &b)
	var d boardDoc
	a.want(200, auth, "GET", fmt.Sprintf("/api/v1/boards/%d", b.ID), nil, &d)
	return d
}

func (a *bapp) newCard(auth string, list int64, body map[string]any) store.Card {
	a.t.Helper()
	var c store.Card
	a.want(201, auth, "POST", fmt.Sprintf("/api/v1/lists/%d/cards", list), body, &c)
	return c
}

func TestBoardsArePerPerson(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Alice's plans")
	if len(d.Lists) != 3 || !d.Lists[2].IsDone || len(d.Labels) != 6 {
		t.Fatalf("default board = %d lists (done %v), %d labels", len(d.Lists), d.Lists[len(d.Lists)-1].IsDone, len(d.Labels))
	}
	c := a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Secret card", "description": "- [ ] one"})
	var cm store.Comment
	a.want(201, a.alice, "POST", fmt.Sprintf("/api/v1/cards/%d/comments", c.ID), map[string]any{"message": "hi"}, &cm)
	att := a.upload(a.alice, c.ID, "note.txt", "hello")

	// Bob sees none of it: everything is 404, never 403.
	b, l, cd := d.Board.ID, d.Lists[0].ID, c.ID
	for _, p := range []struct{ m, path string }{
		{"GET", fmt.Sprintf("/api/v1/boards/%d", b)},
		{"PUT", fmt.Sprintf("/api/v1/boards/%d", b)},
		{"DELETE", fmt.Sprintf("/api/v1/boards/%d", b)},
		{"GET", fmt.Sprintf("/api/v1/boards/%d/activity", b)},
		{"GET", fmt.Sprintf("/api/v1/boards/%d/members", b)},
		{"POST", fmt.Sprintf("/api/v1/boards/%d/members", b)},
		{"POST", fmt.Sprintf("/api/v1/boards/%d/lists", b)},
		{"POST", fmt.Sprintf("/api/v1/boards/%d/labels", b)},
		{"POST", fmt.Sprintf("/api/v1/boards/%d/copy", b)},
		{"PUT", fmt.Sprintf("/api/v1/lists/%d", l)},
		{"DELETE", fmt.Sprintf("/api/v1/lists/%d", l)},
		{"POST", fmt.Sprintf("/api/v1/lists/%d/cards", l)},
		{"GET", fmt.Sprintf("/api/v1/cards/%d", cd)},
		{"PUT", fmt.Sprintf("/api/v1/cards/%d", cd)},
		{"DELETE", fmt.Sprintf("/api/v1/cards/%d", cd)},
		{"GET", fmt.Sprintf("/api/v1/cards/%d/comments", cd)},
		{"POST", fmt.Sprintf("/api/v1/cards/%d/comments", cd)},
		{"PUT", fmt.Sprintf("/api/v1/comments/%d", cm.ID)},
		{"GET", fmt.Sprintf("/api/v1/attachments/%d", att.ID)},
		{"DELETE", fmt.Sprintf("/api/v1/attachments/%d", att.ID)},
		{"PUT", fmt.Sprintf("/api/v1/labels/%d", d.Labels[0].ID)},
		{"PUT", fmt.Sprintf("/api/v1/cards/%d/checklist/0", cd)},
		{"POST", fmt.Sprintf("/api/v1/cards/%d/copy", cd)},
		{"GET", fmt.Sprintf("/api/v1/boards/%d/events", b)},
	} {
		body := map[string]any{"title": "x", "message": "x", "done": true}
		if code, raw := a.do(a.bob, p.m, p.path, body); code != http.StatusNotFound {
			t.Errorf("bob %s %s = %d, want 404: %s", p.m, p.path, code, raw)
		}
	}
	// Nor in lists, search, My cards, the bin, the overlay or the calendar feed.
	for _, p := range []string{"/api/v1/boards", "/api/v1/home", "/api/v1/search?q=Secret", "/api/v1/cards?assigned=0", "/api/v1/trash", "/api/v1/overlay"} {
		if _, raw := a.do(a.bob, "GET", p, nil); strings.Contains(string(raw), "Secret") || strings.Contains(string(raw), "Alice's plans") {
			t.Errorf("bob sees Alice's board in %s: %s", p, raw)
		}
	}
	// Moving one's own card onto someone else's list is refused too.
	bd := a.newBoard(a.bob, "Bob's")
	bc := a.newCard(a.bob, bd.Lists[0].ID, map[string]any{"title": "mine"})
	if code, _ := a.do(a.bob, "PUT", fmt.Sprintf("/api/v1/cards/%d", bc.ID), map[string]any{"list_id": l}); code != http.StatusBadRequest {
		t.Errorf("moving onto a foreign list = %d, want 400", code)
	}
	// The admin doesn't see Bob's board either.
	if code, _ := a.do(a.alice, "GET", fmt.Sprintf("/api/v1/boards/%d", bd.Board.ID), nil); code != http.StatusNotFound {
		t.Errorf("admin GET bob's board = %d, want 404", code)
	}
}

func (a *bapp) upload(auth string, card int64, name, content string) store.Attachment {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/cards/%d/attachments", a.url, card), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var at store.Attachment
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 201 {
		a.t.Fatalf("upload = %d: %s", res.StatusCode, raw)
	}
	_ = json.Unmarshal(raw, &at)
	return at
}

func TestSharingRoles(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Home")
	c := a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Fix the tap"})
	bobID := a.ids["Bob"]

	a.want(200, a.alice, "POST", fmt.Sprintf("/api/v1/boards/%d/members", d.Board.ID), map[string]any{"email": "bob@example.com", "role": "viewer"}, nil)
	var seen boardDoc
	a.want(200, a.bob, "GET", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), nil, &seen)
	if seen.Board.Role != "viewer" || len(seen.Members) != 2 {
		t.Fatalf("bob's view: role %q, %d members", seen.Board.Role, len(seen.Members))
	}
	// A viewer can't change content or share, but can comment and star it for themselves.
	a.want(403, a.bob, "PUT", fmt.Sprintf("/api/v1/cards/%d", c.ID), map[string]any{"title": "x"}, nil)
	a.want(403, a.bob, "POST", fmt.Sprintf("/api/v1/lists/%d/cards", d.Lists[0].ID), map[string]any{"title": "x"}, nil)
	a.want(403, a.bob, "POST", fmt.Sprintf("/api/v1/boards/%d/members", d.Board.ID), map[string]any{"user_id": bobID, "role": "editor"}, nil)
	a.want(201, a.bob, "POST", fmt.Sprintf("/api/v1/cards/%d/comments", c.ID), map[string]any{"message": "I can do it Saturday"}, nil)
	var nb store.Board
	a.want(200, a.bob, "PUT", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), map[string]any{"starred": true}, &nb)
	if !nb.Starred {
		t.Fatal("star not saved")
	}
	var ab store.Board
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), map[string]any{}, &ab)
	if ab.Starred {
		t.Fatal("bob's star shows up for alice")
	}

	// As editor Bob can change cards and be assigned.
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/boards/%d/members/%d", d.Board.ID, bobID), map[string]any{"role": "editor"}, nil)
	a.want(200, a.bob, "PUT", fmt.Sprintf("/api/v1/cards/%d", c.ID), map[string]any{"title": "Fix the kitchen tap"}, nil)
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d/members/%d", c.ID, bobID), nil, nil)
	var mine []store.Card
	a.want(200, a.bob, "GET", "/api/v1/cards?assigned=1", nil, &mine)
	if len(mine) != 1 || mine[0].Title != "Fix the kitchen tap" {
		t.Fatalf("bob's cards = %+v", mine)
	}
	// Someone who can't see the board can't be assigned.
	var carol store.User = store.User{Email: "carol@example.com", Name: "Carol", Role: "member"}
	_ = a.st.CreateUser(context.Background(), &carol)
	a.want(400, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d/members/%d", c.ID, carol.ID), nil, nil)
	// Only the owner deletes; a member's DELETE leaves the board (and drops their assignments).
	a.want(200, a.bob, "DELETE", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), nil, nil)
	a.want(404, a.bob, "GET", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), nil, nil)
	var cd struct {
		Card store.Card `json:"card"`
	}
	a.want(200, a.alice, "GET", fmt.Sprintf("/api/v1/cards/%d", c.ID), nil, &cd)
	if len(cd.Card.Members) != 0 {
		t.Fatalf("bob still assigned after leaving: %v", cd.Card.Members)
	}
}

func TestDoneListsRepeatAndChecklist(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Chores")
	todo, done := d.Lists[0], d.Lists[2]
	due := time.Now().Add(-time.Hour).Format(time.RFC3339)
	c := a.newCard(a.alice, todo.ID, map[string]any{"title": "Water plants", "due_at": due, "repeat": "weekly", "description": "- [x] kitchen\n- [ ] balcony"})
	if c.ChecklistDone != 1 || c.ChecklistTotal != 2 {
		t.Fatalf("checklist %d/%d", c.ChecklistDone, c.ChecklistTotal)
	}
	var ticked store.Card
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d/checklist/1", c.ID), map[string]any{"done": true}, &ticked)
	if ticked.ChecklistDone != 2 || !strings.Contains(ticked.Description, "- [x] balcony") {
		t.Fatalf("tick: %+v", ticked.Description)
	}
	a.want(404, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d/checklist/5", c.ID), map[string]any{"done": true}, nil)

	// Into the done list: done, and the next one appears in To do, unticked, a week later.
	var moved store.Card
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d", c.ID), map[string]any{"list_id": done.ID, "position": 0}, &moved)
	if !moved.Done || moved.Repeat != "" {
		t.Fatalf("moved into done list: done=%v repeat=%q", moved.Done, moved.Repeat)
	}
	var bd boardDoc
	a.want(200, a.alice, "GET", fmt.Sprintf("/api/v1/boards/%d", d.Board.ID), nil, &bd)
	if len(bd.Lists[0].Cards) != 1 {
		t.Fatalf("to do has %d cards, want the next one", len(bd.Lists[0].Cards))
	}
	next := bd.Lists[0].Cards[0]
	nd, _ := time.Parse(time.RFC3339, next.DueAt)
	if next.Repeat != "weekly" || next.Done || next.ChecklistDone != 0 || !nd.After(time.Now()) {
		t.Fatalf("next = %+v", next)
	}
	// Back out of the done list: not done.
	var back store.Card
	a.want(200, a.alice, "PUT", fmt.Sprintf("/api/v1/cards/%d", c.ID), map[string]any{"list_id": todo.ID}, &back)
	if back.Done {
		t.Fatal("still done after leaving the done list")
	}
}

func TestQuickAdd(t *testing.T) {
	labels := []store.Label{{ID: 1, Title: "Bug"}, {ID: 2, Title: "Quick win"}}
	members := []store.Member{{UserID: 7, Name: "Mia Novak", Email: "mia@example.com"}, {UserID: 3, Name: "Ana", Email: "ana@example.com"}}
	now := time.Date(2026, 10, 31, 18, 0, 0, 0, time.Local) // a Saturday
	q := parseQuick("Fix the footer #bug #quick_win @mia !fri !9:30 #nope", labels, members, 3, "17:00", now)
	if q.title != "Fix the footer #nope" || len(q.labels) != 2 || len(q.members) != 1 || q.members[0] != 7 {
		t.Fatalf("parsed %+v", q)
	}
	if q.due != time.Date(2026, 11, 6, 9, 30, 0, 0, time.Local).Format(time.RFC3339) {
		t.Fatalf("due = %s", q.due)
	}
	if q := parseQuick("Call @me !tomorrow", labels, members, 3, "17:00", now); len(q.members) != 1 || q.members[0] != 3 || !strings.HasPrefix(q.due, "2026-11-01T17:00") {
		t.Fatalf("@me / tomorrow: %+v", q)
	}
	if q := parseQuick("Pay !3d", labels, members, 3, "08:00", now); !strings.HasPrefix(q.due, "2026-11-03T08:00") {
		t.Fatalf("!3d: %+v", q)
	}
}

func TestExportImportIdempotent(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Trip")
	c := a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Flights", "labels": []int64{d.Labels[0].ID}})
	a.want(201, a.alice, "POST", fmt.Sprintf("/api/v1/cards/%d/comments", c.ID), map[string]any{"message": "cheap on Tuesdays"}, nil)
	_, raw := a.do(a.alice, "GET", "/api/v1/export.json", nil)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)

	var first, second map[string]int
	a.want(200, a.bob, "POST", "/api/v1/import", doc, &first)
	a.want(200, a.bob, "POST", "/api/v1/import", doc, &second)
	if first["boards"] != 1 || first["cards"] != 1 || first["comments"] != 1 || first["labels"] != 6 {
		t.Fatalf("first import = %v", first)
	}
	if second["boards"] != 0 || second["cards"] != 0 || second["skipped"] != 1 {
		t.Fatalf("second import = %v", second)
	}
	var boards []store.Board
	a.want(200, a.bob, "GET", "/api/v1/boards", nil, &boards)
	if len(boards) != 1 || boards[0].Title != "Trip" || boards[0].OwnerID != a.ids["Bob"] {
		t.Fatalf("bob's boards = %+v", boards)
	}
	// Exports only hold one's own boards.
	_, raw = a.do(a.bob, "GET", "/api/v1/export.json", nil)
	if strings.Count(string(raw), `"title":"Trip"`) != 1 {
		t.Fatalf("bob's export: %s", raw)
	}
}

func TestSettingsArePersonal(t *testing.T) {
	a := newBoardsApp(t)
	a.want(200, a.alice, "PUT", "/api/v1/settings", map[string]any{"card_density": "compact", "remind_before_min": 30, "title": "Our boards"}, nil)
	a.want(200, a.bob, "PUT", "/api/v1/settings", map[string]any{"title": "Bob's title"}, nil)
	var al, bo map[string]any
	a.want(200, a.alice, "GET", "/api/v1/settings", nil, &al)
	a.want(200, a.bob, "GET", "/api/v1/settings", nil, &bo)
	if al["card_density"] != "compact" || bo["card_density"] != "comfortable" || al["remind_before_min"].(float64) != 30 {
		t.Fatalf("personal settings leaked: alice %v / bob %v", al["card_density"], bo["card_density"])
	}
	if bo["title"] != "Our boards" {
		t.Fatalf("box title = %v (a member may not change it)", bo["title"])
	}
	if al["ics_url"] == bo["ics_url"] || !strings.Contains(al["ics_url"].(string), "token=") {
		t.Fatalf("calendar feeds: %v / %v", al["ics_url"], bo["ics_url"])
	}
	a.want(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"due_time_default": "25:00"}, nil)
}

func TestCalendarFeed(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Work")
	a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Send the invoice, today", "due_at": time.Now().Add(2 * time.Hour).Format(time.RFC3339)})
	var st map[string]any
	a.want(200, a.alice, "GET", "/api/v1/settings", nil, &st)
	u := strings.TrimPrefix(st["ics_url"].(string), "http://boards.test")
	code, raw := a.do("", "GET", u, nil)
	if code != 200 || !strings.Contains(string(raw), `SUMMARY:Send the invoice\, today`) {
		t.Fatalf("feed = %d: %s", code, raw)
	}
	if code, _ := a.do("", "GET", "/api/v1/calendar.ics?token=wrongwrongwrongwrongwrong", nil); code != 404 {
		t.Fatalf("wrong token = %d", code)
	}
	var fresh map[string]string
	a.want(200, a.alice, "POST", "/api/v1/settings/ics-token", nil, &fresh)
	if code, _ := a.do("", "GET", u, nil); code != 404 {
		t.Fatal("the old feed address still works after making a new one")
	}
}

func TestLinkScopes(t *testing.T) {
	cases := []struct {
		scopes []string
		method string
		path   string
		ok     bool
	}{
		{nil, "GET", "/api/v1/auth/me", true},
		{[]string{"boards:read"}, "GET", "/api/v1/boards/3", true},
		{[]string{"boards:read"}, "GET", "/api/v1/cards", true},
		{[]string{"boards:read"}, "PUT", "/api/v1/cards/3", false},
		{[]string{"boards:write"}, "POST", "/api/v1/lists/2/cards", true},
		{[]string{"boards:write"}, "PUT", "/api/v1/cards/3", true},
		{[]string{"boards:write"}, "DELETE", "/api/v1/cards/3", false},
		{[]string{"boards:write"}, "DELETE", "/api/v1/boards/3", false},
		{[]string{"boards:write"}, "POST", "/api/v1/boards/3/members", false},
		{[]string{"boards:read", "boards:write"}, "GET", "/api/v1/attachments/1", false},
		{[]string{"worklog:write"}, "POST", "/api/v1/worklog", true},
		{[]string{"worklog:read"}, "POST", "/api/v1/worklog", false},
		{[]string{"worklog:read"}, "GET", "/api/v1/worklog/items", true},
		{[]string{"boards:read"}, "GET", "/api/v1/settings", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := linkAllows(c.scopes, r); got != c.ok {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.method, c.path, got, c.ok)
		}
	}
}

func TestWorklogFromPocketTime(t *testing.T) {
	a := newBoardsApp(t)
	d := a.newBoard(a.alice, "Work")
	c := a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Write report"})
	body := map[string]any{"ref": fmt.Sprintf("card:%d", c.ID), "date": "2026-10-30", "duration_s": 1800, "external_id": "time:5"}
	a.want(200, a.alice, "POST", "/api/v1/worklog", body, nil)
	body["duration_s"] = 2400
	a.want(200, a.alice, "POST", "/api/v1/worklog", body, nil) // same external id: updated, not added
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	a.want(200, a.alice, "GET", "/api/v1/worklog?from=2026-10-01&to=2026-10-31", nil, &out)
	if len(out.Entries) != 1 || out.Entries[0]["duration_s"].(float64) != 2400 {
		t.Fatalf("worklog = %+v", out.Entries)
	}
	a.want(404, a.bob, "POST", "/api/v1/worklog", body, nil)
	a.want(400, a.alice, "POST", "/api/v1/worklog", map[string]any{"ref": "issue:1", "duration_s": 5}, nil)
}
