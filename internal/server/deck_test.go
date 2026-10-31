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
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- helpers (deck-prefixed so they don't clash with boards_test.go) ----

type deckApp struct {
	t              *testing.T
	url            string
	s              *Server
	st             *store.Store
	alice, bob     string
	aliceID, bobID int64
}

const deckAlicePassword = "alice-password-1"

func newDeckApp(t *testing.T) *deckApp {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.MaxBodyBytes = 64 << 20
	st := openTestStore(t, cfg)
	ts := httptest.NewUnstartedServer(nil)
	cfg.BaseURL = "http://" + ts.Listener.Addr().String()
	ctx := context.Background()
	a := &deckApp{t: t, st: st, url: cfg.BaseURL}
	hash, _ := bcrypt.GenerateFromPassword([]byte(deckAlicePassword), bcrypt.MinCost)
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin", PasswordHash: string(hash)}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, err := st.CreateAPIToken(ctx, u.ID, "t")
		if err != nil {
			t.Fatal(err)
		}
		if u.Name == "Alice" {
			a.alice, a.aliceID = tok, u.ID
		} else {
			a.bob, a.bobID = tok, u.ID
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = a.s.Router()
	ts.Start()
	t.Cleanup(ts.Close)
	return a
}

type deckAuthFn func(*http.Request)

func deckBasic(user, pass string) deckAuthFn {
	return func(r *http.Request) { r.SetBasicAuth(user, pass) }
}

func deckBearer(tok string) deckAuthFn {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

// do sends a request; a non-reader body is sent as JSON.
func (a *deckApp) do(auth deckAuthFn, method, path string, body any, ctype string) (int, []byte, http.Header) {
	a.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rd = b
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
		ctype = "application/json"
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("OCS-APIRequest", "true")
	if auth != nil {
		auth(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw, res.Header
}

func (a *deckApp) json(auth deckAuthFn, method, path string, body any, want int) any {
	a.t.Helper()
	code, raw, _ := a.do(auth, method, path, body, "")
	if code != want {
		a.t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, raw)
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { l, _ := v.([]any); return l }
func num(v any) int64          { f, _ := v.(float64); return int64(f) }

func deckUpload(t *testing.T, field, name, content string, extra map[string]string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, name)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

const deckV1 = "/index.php/apps/deck/api/v1.0"

// ---- the Deck-compatible API ----

func TestDeckAPI(t *testing.T) {
	a := newDeckApp(t)
	alice := deckBasic("alice@example.com", a.alice)

	// Clients recognise the server without signing in.
	st := obj(a.json(nil, "GET", "/status.php", nil, 200))
	if st["installed"] != true || st["productname"] != "Pocket Boards" {
		t.Fatalf("status.php: %v", st)
	}
	caps := obj(obj(obj(a.json(nil, "GET", "/ocs/v2.php/cloud/capabilities", nil, 200))["ocs"])["data"])
	if obj(obj(caps["capabilities"])["deck"])["version"] == nil {
		t.Fatalf("capabilities: %v", caps)
	}

	// No or wrong credentials.
	if code, _, h := a.do(nil, "GET", deckV1+"/boards", nil, ""); code != 401 || h.Get("WWW-Authenticate") == "" {
		t.Fatalf("no auth: %d", code)
	}
	if code, _, _ := a.do(deckBasic("alice@example.com", "wrong"), "GET", deckV1+"/boards", nil, ""); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if l := arr(a.json(deckBasic("alice@example.com", deckAlicePassword), "GET", deckV1+"/boards", nil, 200)); len(l) != 0 {
		t.Fatalf("password sign-in should list no boards yet: %v", l)
	}
	me := obj(obj(obj(a.json(alice, "GET", "/ocs/v2.php/cloud/user", nil, 200))["ocs"])["data"])
	if me["id"] != "alice@example.com" {
		t.Fatalf("cloud/user: %v", me)
	}

	// Board, stacks, card.
	board := obj(a.json(alice, "POST", deckV1+"/boards", map[string]any{"title": "Deck test", "color": "ff0000"}, 200))
	bid := num(board["id"])
	if board["color"] != "ff0000" || len(arr(board["labels"])) != 4 || obj(board["permissions"])["PERMISSION_MANAGE"] != true {
		t.Fatalf("board: %v", board)
	}
	todo := obj(a.json(alice, "POST", fmt.Sprintf("%s/boards/%d/stacks", deckV1, bid), map[string]any{"title": "To do", "order": 0}, 200))
	done := obj(a.json(alice, "POST", fmt.Sprintf("%s/boards/%d/stacks", deckV1, bid), map[string]any{"title": "Done", "order": 1}, 200))
	tid, did := num(todo["id"]), num(done["id"])
	cardPath := func(stack, card int64) string {
		return fmt.Sprintf("%s/boards/%d/stacks/%d/cards/%d", deckV1, bid, stack, card)
	}
	card := obj(a.json(alice, "POST", fmt.Sprintf("%s/boards/%d/stacks/%d/cards", deckV1, bid, tid),
		map[string]any{"title": "First", "type": "plain", "order": 999, "description": "- [ ] a", "duedate": "2026-12-01T10:00:00+00:00"}, 200))
	cid := num(card["id"])
	if card["duedate"] != "2026-12-01T10:00:00+00:00" || num(card["stackId"]) != tid || card["done"] != nil {
		t.Fatalf("card: %v", card)
	}
	// Form-encoded update (some clients send forms), then JSON.
	form := url.Values{"title": {"First card"}, "type": {"plain"}, "owner": {"alice@example.com"}, "description": {"- [x] a"}}
	if code, raw, _ := a.do(alice, "PUT", cardPath(tid, cid), strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"); code != 200 || !strings.Contains(string(raw), "First card") {
		t.Fatalf("form update: %d %s", code, raw)
	}
	a.json(alice, "PUT", cardPath(tid, cid), map[string]any{"title": "First card", "type": "plain", "duedate": nil, "done": "2026-11-01T10:00:00+00:00"}, 200)
	c, _ := a.st.CardByID(context.Background(), cid)
	if c.DueAt != "" || c.DoneAt == "" || c.Description != "- [x] a" {
		t.Fatalf("after update: due=%q done=%q desc=%q", c.DueAt, c.DoneAt, c.Description)
	}
	a.json(alice, "PUT", cardPath(tid, cid)+"/undone", nil, 200)

	// Move it to Done.
	moved := arr(a.json(alice, "PUT", cardPath(tid, cid)+"/reorder", map[string]any{"order": 0, "stackId": did}, 200))
	if len(moved) != 1 || num(obj(moved[0])["stackId"]) != did {
		t.Fatalf("reorder: %v", moved)
	}

	// Labels and people.
	lid := num(obj(arr(board["labels"])[0])["id"])
	a.json(alice, "PUT", cardPath(did, cid)+"/assignLabel", map[string]any{"labelId": lid}, 200)
	a.json(alice, "PUT", cardPath(did, cid)+"/assignUser", map[string]any{"userId": "alice@example.com"}, 200)
	a.json(alice, "PUT", cardPath(did, cid)+"/assignUser", map[string]any{"userId": "alice@example.com"}, 400)
	a.json(alice, "PUT", cardPath(did, cid)+"/assignUser", map[string]any{"userId": "bob@example.com"}, 400) // not on the board
	got := obj(a.json(alice, "GET", cardPath(did, cid), nil, 200))
	if len(arr(got["labels"])) != 1 || len(arr(got["assignedUsers"])) != 1 || obj(obj(arr(got["assignedUsers"])[0])["participant"])["uid"] != "alice@example.com" {
		t.Fatalf("card after label + user: %v", got)
	}
	// Same thing through the v1.1 path without index.php.
	a.json(alice, "GET", fmt.Sprintf("/apps/deck/api/v1.1/boards/%d/stacks/%d/cards/%d", bid, did, cid), nil, 200)

	// Comments (OCS).
	ocs := fmt.Sprintf("/ocs/v2.php/apps/deck/api/v1.0/cards/%d/comments", cid)
	cm := obj(obj(obj(a.json(alice, "POST", ocs, map[string]any{"message": "hi there"}, 200))["ocs"])["data"])
	if cm["message"] != "hi there" || cm["actorId"] != "alice@example.com" {
		t.Fatalf("comment: %v", cm)
	}
	a.json(alice, "POST", ocs, map[string]any{"message": "a reply", "parentId": num(cm["id"])}, 200)
	list := arr(obj(obj(a.json(alice, "GET", ocs+"?limit=10", nil, 200))["ocs"])["data"])
	if len(list) != 2 || obj(list[0])["replyTo"] == nil {
		t.Fatalf("comments: %v", list)
	}
	v1 := obj(obj(obj(a.json(alice, "GET", strings.Replace(ocs, "v2.php", "v1.php", 1), nil, 200))["ocs"])["meta"])
	if num(v1["statuscode"]) != 100 {
		t.Fatalf("ocs v1 meta: %v", v1)
	}

	// Attachments.
	body, ct := deckUpload(t, "file", "notes.txt", "hello deck", map[string]string{"type": "deck_file"})
	code, raw, _ := a.do(alice, "POST", cardPath(did, cid)+"/attachments", body, ct)
	if code != 200 {
		t.Fatalf("upload: %d %s", code, raw)
	}
	var att map[string]any
	_ = json.Unmarshal(raw, &att)
	aid := num(att["id"])
	if att["data"] != "notes.txt" || num(obj(att["extendedData"])["filesize"]) != 10 {
		t.Fatalf("attachment: %v", att)
	}
	for _, p := range []string{fmt.Sprintf("/attachments/%d", aid), fmt.Sprintf("/attachments/deck_file/%d", aid)} {
		if code, raw, _ := a.do(alice, "GET", cardPath(did, cid)+p, nil, ""); code != 200 || string(raw) != "hello deck" {
			t.Fatalf("download %s: %d %q", p, code, raw)
		}
	}
	if l := arr(a.json(alice, "GET", cardPath(did, cid)+"/attachments", nil, 200)); len(l) != 1 {
		t.Fatalf("attachments: %v", l)
	}
	a.json(alice, "DELETE", cardPath(did, cid)+fmt.Sprintf("/attachments/%d", aid), nil, 200)
	a.json(alice, "PUT", cardPath(did, cid)+fmt.Sprintf("/attachments/%d/restore", aid), nil, 200)

	// Stacks list carries the cards; If-Modified-Since in the future → nothing.
	stacks := arr(a.json(alice, "GET", fmt.Sprintf("%s/boards/%d/stacks", deckV1, bid), nil, 200))
	if len(stacks) != 2 || len(arr(obj(stacks[1])["cards"])) != 1 {
		t.Fatalf("stacks: %v", stacks)
	}
	req, _ := http.NewRequest("GET", a.url+deckV1+"/boards", nil)
	alice(req)
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	res, _ := http.DefaultClient.Do(req)
	var none []any
	_ = json.NewDecoder(res.Body).Decode(&none)
	res.Body.Close()
	if len(none) != 0 {
		t.Fatalf("If-Modified-Since: %v", none)
	}

	// Bob can't see Alice's board, through any path.
	bob := deckBasic("bob@example.com", a.bob)
	a.json(bob, "GET", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 404)
	a.json(bob, "GET", fmt.Sprintf("%s/boards/%d/stacks", deckV1, bid), nil, 404)
	a.json(bob, "GET", cardPath(did, cid), nil, 404)
	if code, _, _ := a.do(bob, "GET", ocs, nil, ""); code != 404 {
		t.Fatalf("bob comments: %d", code)
	}
	if l := arr(a.json(bob, "GET", deckV1+"/boards", nil, 200)); len(l) != 0 {
		t.Fatalf("bob sees boards: %v", l)
	}

	// Shared read-only: Bob sees it, can't change it.
	acl := arr(a.json(alice, "POST", fmt.Sprintf("%s/boards/%d/acl", deckV1, bid), map[string]any{"type": 0, "participant": "bob@example.com", "permissionEdit": false}, 200))
	if len(acl) != 1 {
		t.Fatalf("acl: %v", acl)
	}
	bb := obj(a.json(bob, "GET", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 200))
	if obj(bb["permissions"])["PERMISSION_EDIT"] != false || num(bb["shared"]) != 1 {
		t.Fatalf("bob's view: %v", bb)
	}
	a.json(bob, "POST", fmt.Sprintf("%s/boards/%d/stacks/%d/cards", deckV1, bid, tid), map[string]any{"title": "nope"}, 403)
	a.json(bob, "DELETE", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 403)
	a.json(alice, "PUT", fmt.Sprintf("%s/boards/%d/acl/%d", deckV1, bid, a.bobID), map[string]any{"permissionEdit": true}, 200)
	a.json(bob, "POST", fmt.Sprintf("%s/boards/%d/stacks/%d/cards", deckV1, bid, tid), map[string]any{"title": "yes"}, 200)

	// Delete and undo.
	a.json(alice, "DELETE", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 200)
	a.json(bob, "GET", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 404)
	a.json(alice, "POST", fmt.Sprintf("%s/boards/%d/undo_delete", deckV1, bid), nil, 200)
	a.json(bob, "GET", fmt.Sprintf("%s/boards/%d", deckV1, bid), nil, 200)

	// Changes made through Deck are marked as such.
	acts, _ := a.st.ListActivity(context.Background(), bid, cid, 0, 50)
	deck := false
	for _, x := range acts {
		deck = deck || x.Via == "deck"
	}
	if !deck {
		t.Fatalf("no activity marked via deck: %v", acts)
	}
}

// ---- Login Flow v2 ----

func TestDeckLoginFlow(t *testing.T) {
	a := newDeckApp(t)
	start := obj(a.json(nil, "POST", "/index.php/login/v2", nil, 200))
	poll := obj(start["poll"])
	login, _ := start["login"].(string)
	if !strings.HasPrefix(login, a.url+"/login/flow/") || poll["endpoint"] != a.url+"/index.php/login/v2/poll" {
		t.Fatalf("start: %v", start)
	}
	loginTok := strings.TrimPrefix(login, a.url+"/login/flow/")
	pollForm := func() (int, map[string]any) {
		code, raw, _ := a.do(nil, "POST", "/index.php/login/v2/poll", strings.NewReader("token="+url.QueryEscape(poll["token"].(string))), "application/x-www-form-urlencoded")
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return code, m
	}
	if code, _ := pollForm(); code != 404 {
		t.Fatalf("poll before approval: %d", code)
	}
	a.json(nil, "GET", "/api/v1/loginflow/"+loginTok, nil, 401)
	info := obj(a.json(deckBearer(a.alice), "GET", "/api/v1/loginflow/"+loginTok, nil, 200))
	if info["client_name"] == "" {
		t.Fatalf("info: %v", info)
	}
	a.json(deckBearer(a.alice), "POST", "/api/v1/loginflow/"+loginTok+"/approve", nil, 200)
	a.json(deckBearer(a.bob), "POST", "/api/v1/loginflow/"+loginTok+"/approve", nil, 404) // used already
	code, got := pollForm()
	if code != 200 || got["loginName"] != "alice@example.com" || got["server"] != a.url {
		t.Fatalf("poll: %d %v", code, got)
	}
	if code, _ := pollForm(); code != 404 {
		t.Fatalf("second poll should be gone: %d", code)
	}
	pass, _ := got["appPassword"].(string)
	a.json(deckBasic("alice@example.com", pass), "GET", deckV1+"/boards", nil, 200)
	toks, _ := a.st.ListAPITokens(context.Background(), a.aliceID)
	named := false
	for _, tk := range toks {
		named = named || strings.HasPrefix(tk.Name, "Deck: ")
	}
	if !named {
		t.Fatalf("no Deck token listed: %v", toks)
	}
}

// ---- Trello import ----

func (a *deckApp) rows(q string, args ...any) int {
	a.t.Helper()
	var n int
	if err := a.st.DB.QueryRow(q, args...).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func TestTrelloImport(t *testing.T) {
	a := newDeckApp(t)
	raw, err := os.ReadFile("testdata/trello.json")
	if err != nil {
		t.Fatal(err)
	}
	first := obj(a.json(deckBearer(a.alice), "POST", "/api/v1/import/trello", bytes.NewReader(raw), 200))
	want := map[string]int64{"boards": 1, "lists": 2, "labels": 3, "cards": 4, "comments": 2, "skipped": 1}
	for k, v := range want {
		if num(first[k]) != v {
			t.Fatalf("first import %s = %v, want %d (%v)", k, first[k], v, first)
		}
	}
	counts := func() [4]int {
		return [4]int{a.rows(`SELECT COUNT(1) FROM boards`), a.rows(`SELECT COUNT(1) FROM lists`), a.rows(`SELECT COUNT(1) FROM cards`), a.rows(`SELECT COUNT(1) FROM comments`)}
	}
	before := counts()
	// Multipart the second time.
	body, ct := deckUpload(t, "file", "trello.json", string(raw), nil)
	code, out, _ := a.do(deckBearer(a.alice), "POST", "/api/v1/import/trello", body, ct)
	if code != 200 {
		t.Fatalf("second import: %d %s", code, out)
	}
	var second map[string]any
	_ = json.Unmarshal(out, &second)
	if num(second["boards"])+num(second["cards"])+num(second["comments"])+num(second["lists"]) != 0 || counts() != before {
		t.Fatalf("second import added rows: %v %v→%v", second, before, counts())
	}

	ctx := context.Background()
	b, err := a.st.BoardByExternal(ctx, a.aliceID, "trello", "trello:5f1a")
	if err != nil || b.Title != "Garden" || b.Color != "519839" {
		t.Fatalf("board: %+v %v", b, err)
	}
	lists, _ := a.st.Lists(ctx, b.ID, false)
	if len(lists) != 2 || lists[0].Title != "To do" || !lists[1].IsDone {
		t.Fatalf("lists: %+v", lists)
	}
	labels, _ := a.st.Labels(ctx, b.ID)
	colors := map[string]string{}
	for _, l := range labels {
		colors[l.Title] = l.Color
	}
	if colors["Urgent"] != "f87168" || colors[""] != "4bce97" || colors["Someday"] != "8590a2" {
		t.Fatalf("label colours: %v", colors)
	}
	todo, _ := a.st.ListCards(ctx, lists[0].ID, false)
	if len(todo) != 2 || todo[0].Title != "Buy soil" || todo[1].Title != "Plant tomatoes" {
		t.Fatalf("to do cards: %+v", todo)
	}
	tom := todo[1]
	if !strings.Contains(tom.Description, "### Steps\n- [x] Dig\n- [ ] Water") || !strings.Contains(tom.Description, "[Guide](https://example.com/tomatoes)") ||
		tom.ChecklistTotal != 2 || tom.CoverColor != "e774bb" || len(tom.Labels) != 2 || tom.DueAt == "" {
		t.Fatalf("tomatoes: %+v", tom)
	}
	cs, _ := a.st.Comments(ctx, tom.ID)
	if len(cs) != 2 || cs[0].Message != "**Tom Green:** Which variety?" || cs[1].Message != "Done digging" {
		t.Fatalf("comments: %+v", cs)
	}
	arch, _ := a.st.ListCards(ctx, lists[0].ID, true)
	if len(arch) != 1 || arch[0].Title != "Old card" {
		t.Fatalf("archived: %+v", arch)
	}
	done, _ := a.st.ListCards(ctx, lists[1].ID, false)
	if len(done) != 1 || !done[0].Done {
		t.Fatalf("done: %+v", done)
	}
	// Bob doesn't see it.
	a.json(deckBearer(a.bob), "GET", fmt.Sprintf("/api/v1/boards/%d", b.ID), nil, 404)
	a.json(deckBearer(a.alice), "POST", "/api/v1/import/trello", map[string]any{"name": "not trello"}, 400)
}

// ---- Deck import from a fake Nextcloud ----

type fakeDeck struct {
	mu    sync.Mutex
	title string
	hits  map[string]int
}

func newFakeDeck(t *testing.T) (*fakeDeck, *httptest.Server) {
	f := &fakeDeck{title: "Family", hits: map[string]int{}}
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	user := func(uid, name string) map[string]any {
		return map[string]any{"primaryKey": uid, "uid": uid, "displayname": name}
	}
	api := "/index.php/apps/deck/api/v1.0"
	card := func(id, stack int64, title string, order int, extra map[string]any) map[string]any {
		c := map[string]any{"id": id, "title": title, "description": nil, "stackId": stack, "type": "plain", "order": order, "archived": false, "duedate": nil,
			"done": nil, "deletedAt": 0, "createdAt": 1767225600, "lastModified": 1767225600, "labels": []any{}, "assignedUsers": []any{}, "owner": "bob-nc"}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "bob-nc" || p != "secret" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		f.hits[r.URL.Path]++
		title := f.title
		f.mu.Unlock()
		switch r.URL.Path {
		case "/status.php":
			j(w, map[string]any{"installed": true, "versionstring": "29.0.4", "productname": "Nextcloud"})
		case api + "/boards":
			j(w, []any{
				map[string]any{"id": 1, "title": title, "color": "0082c9", "archived": false, "deletedAt": 0, "owner": user("bob-nc", "Bob")},
				map[string]any{"id": 2, "title": "Deleted", "color": "000000", "archived": false, "deletedAt": 1700000000, "owner": user("bob-nc", "Bob")},
			})
		case api + "/boards/1":
			j(w, map[string]any{"id": 1, "title": title, "color": "0082c9", "archived": false, "deletedAt": 0, "owner": user("bob-nc", "Bob"),
				"labels": []any{map[string]any{"id": 5, "title": "Urgent", "color": "FF0000"}},
				"users":  []any{user("bob-nc", "Bob"), user("stranger", "Stranger")},
				"acl":    []any{map[string]any{"type": 0, "participant": user("stranger", "Stranger"), "permissionEdit": true}}})
		case api + "/boards/1/stacks":
			j(w, []any{
				map[string]any{"id": 10, "title": "To do", "order": 0, "deletedAt": 0, "cards": []any{
					card(100, 10, "Card A", 1, map[string]any{"description": "- [ ] x", "duedate": "2026-12-01T10:00:00+00:00",
						"labels": []any{map[string]any{"id": 5, "title": "Urgent", "color": "FF0000"}}, "assignedUsers": []any{map[string]any{"participant": user("bob-nc", "Bob")}}}),
					card(101, 10, "Card B", 0, nil),
				}},
				map[string]any{"id": 11, "title": "Done ✅", "order": 1, "deletedAt": 0, "cards": []any{
					card(102, 11, "Finished thing", 0, map[string]any{"done": "2026-10-01T10:00:00+00:00"}),
				}},
			})
		case api + "/boards/1/stacks/archived":
			j(w, []any{map[string]any{"id": 10, "title": "To do", "order": 0, "cards": []any{card(103, 10, "Archived one", 5, map[string]any{"archived": true})}}})
		case "/ocs/v2.php/apps/deck/api/v1.0/cards/100/comments":
			data := []any{}
			if r.URL.Query().Get("offset") == "0" {
				data = append(data, map[string]any{"id": 900, "message": "Hello", "actorId": "stranger", "actorDisplayName": "Stranger", "creationDateTime": "2026-10-01T10:00:00+00:00"})
			}
			j(w, map[string]any{"ocs": map[string]any{"meta": map[string]any{"status": "ok"}, "data": data}})
		case "/index.php/apps/deck/api/v1.1/boards/1/stacks/10/cards/100/attachments":
			j(w, []any{map[string]any{"id": 7, "type": "file", "data": "notes.txt", "deletedAt": 0, "extendedData": map[string]any{"filesize": 5, "mimetype": "text/plain"}}})
		case "/index.php/apps/deck/api/v1.1/boards/1/stacks/10/cards/100/attachments/file/7":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello"))
		default:
			if strings.HasPrefix(r.URL.Path, "/ocs/") {
				j(w, map[string]any{"ocs": map[string]any{"data": []any{}}})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/attachments") {
				j(w, []any{})
				return
			}
			t.Logf("fake deck: unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return f, ts
}

func (a *deckApp) setNextcloud(uid int64, base, user, pass string) {
	a.t.Helper()
	ctx := context.Background()
	st, _ := a.st.GetSettings(ctx, uid)
	st.NextcloudMode, st.NextcloudURL, st.NextcloudUser, st.NextcloudPassword = "direct", base, user, pass
	if err := a.st.SaveUserSettings(ctx, uid, st); err != nil {
		a.t.Fatal(err)
	}
}

func TestDeckImportFromNextcloud(t *testing.T) {
	a := newDeckApp(t)
	fake, ts := newFakeDeck(t)
	bob := deckBearer(a.bob)

	// Not set up yet.
	a.json(bob, "GET", "/api/v1/import/deck/boards", nil, 400)
	a.setNextcloud(a.bobID, ts.URL, "bob-nc", "secret")
	test := obj(a.json(bob, "POST", "/api/v1/integrations/test", map[string]any{"which": "nextcloud"}, 200))
	if test["ok"] != true || !strings.Contains(fmt.Sprint(test["message"]), "Deck: 1 boards") {
		t.Fatalf("test: %v", test)
	}
	list := arr(a.json(bob, "GET", "/api/v1/import/deck/boards", nil, 200))
	if len(list) != 1 || obj(list[0])["imported_board_id"] != nil {
		t.Fatalf("boards: %v", list)
	}
	res := obj(a.json(bob, "POST", "/api/v1/import/deck", map[string]any{"board_ids": []int{1}}, 200))
	want := map[string]int64{"boards": 1, "lists": 2, "labels": 1, "cards": 4, "comments": 1, "attachments": 1}
	for k, v := range want {
		if num(res[k]) != v {
			t.Fatalf("import %s = %v, want %d (%v)", k, res[k], v, res)
		}
	}
	ctx := context.Background()
	host := strings.TrimPrefix(ts.URL, "http://")
	b, err := a.st.BoardByExternal(ctx, a.bobID, "deck", host+":1")
	if err != nil || b.Title != "Family" || b.Color != "0082c9" {
		t.Fatalf("board: %+v %v", b, err)
	}
	if ms, _ := a.st.MemberIDs(ctx, b.ID); len(ms) != 1 {
		t.Fatalf("a stranger was added: %v", ms)
	}
	lists, _ := a.st.Lists(ctx, b.ID, false)
	if len(lists) != 2 || lists[0].IsDone || !lists[1].IsDone {
		t.Fatalf("lists: %+v", lists)
	}
	todo, _ := a.st.ListCards(ctx, lists[0].ID, false)
	if len(todo) != 2 || todo[0].Title != "Card B" || todo[1].Title != "Card A" {
		t.Fatalf("order: %+v", todo)
	}
	ca := todo[1]
	if ca.Description != "- [ ] x" || len(ca.Labels) != 1 || len(ca.Members) != 1 || ca.Members[0] != a.bobID || ca.DueAt == "" {
		t.Fatalf("card A: %+v", ca)
	}
	if due, _ := parseTime(ca.DueAt); !due.Equal(time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("due: %s", ca.DueAt)
	}
	cs, _ := a.st.Comments(ctx, ca.ID)
	if len(cs) != 1 || cs[0].Message != "**Stranger:** Hello" {
		t.Fatalf("comments: %+v", cs)
	}
	atts, _ := a.st.Attachments(ctx, ca.ID)
	if len(atts) != 1 {
		t.Fatalf("attachments: %+v", atts)
	}
	data, _ := os.ReadFile(a.s.cfg.FilesDir + "/" + atts[0].Stored)
	if string(data) != "hello" {
		t.Fatalf("attachment bytes: %q", data)
	}
	arch, _ := a.st.ListCards(ctx, lists[0].ID, true)
	fin, _ := a.st.ListCards(ctx, lists[1].ID, false)
	if len(arch) != 1 || len(fin) != 1 || !fin[0].Done {
		t.Fatalf("archived %+v / done %+v", arch, fin)
	}

	// Again, with a renamed board: updates, adds nothing.
	fake.mu.Lock()
	fake.title = "Family 2"
	fake.mu.Unlock()
	before := [3]int{a.rows(`SELECT COUNT(1) FROM cards`), a.rows(`SELECT COUNT(1) FROM comments`), a.rows(`SELECT COUNT(1) FROM attachments`)}
	res = obj(a.json(bob, "POST", "/api/v1/import/deck", map[string]any{"board_ids": []int{1}}, 200))
	after := [3]int{a.rows(`SELECT COUNT(1) FROM cards`), a.rows(`SELECT COUNT(1) FROM comments`), a.rows(`SELECT COUNT(1) FROM attachments`)}
	if num(res["boards"]) != 0 || num(res["cards"]) != 0 || num(res["updated_cards"]) != 4 || before != after {
		t.Fatalf("second import: %v %v→%v", res, before, after)
	}
	b, _ = a.st.BoardByExternal(ctx, a.bobID, "deck", host+":1")
	if b.Title != "Family 2" {
		t.Fatalf("title not updated: %s", b.Title)
	}
	status := obj(a.json(bob, "GET", "/api/v1/import/deck/status", nil, 200))
	if status["done"] != true {
		t.Fatalf("status: %v", status)
	}
	// Alice has no import running and sees nothing of Bob's.
	if obj(a.json(deckBearer(a.alice), "GET", "/api/v1/import/deck/status", nil, 200))["done"] == true {
		t.Fatal("alice sees bob's import")
	}
	a.json(deckBearer(a.alice), "GET", fmt.Sprintf("/api/v1/boards/%d", b.ID), nil, 404)
}

// TestDeckImportRoundTrip points the Deck importer at Boards' own
// Deck-compatible API: what comes out must match what went in.
func TestDeckImportRoundTrip(t *testing.T) {
	a := newDeckApp(t)
	alice := deckBearer(a.alice)
	src := obj(a.json(alice, "POST", "/api/v1/boards", map[string]any{"title": "Source", "color": "1f845a"}, 201))
	sid := num(src["id"])
	doc := obj(a.json(alice, "GET", fmt.Sprintf("/api/v1/boards/%d", sid), nil, 200))
	lists := arr(doc["lists"])
	labels := arr(doc["labels"])
	todo, doneL := num(obj(lists[0])["id"]), num(obj(lists[2])["id"])
	l0 := num(obj(labels[0])["id"])
	one := obj(a.json(alice, "POST", fmt.Sprintf("/api/v1/lists/%d/cards", todo), map[string]any{"title": "One", "description": "- [x] a\n- [ ] b", "due_at": "2026-12-01T10:00:00+01:00", "labels": []int64{l0}}, 201))
	oneID := num(one["id"])
	two := obj(a.json(alice, "POST", fmt.Sprintf("/api/v1/lists/%d/cards", todo), map[string]any{"title": "Two"}, 201))
	a.json(alice, "PUT", fmt.Sprintf("/api/v1/cards/%d", num(two["id"])), map[string]any{"archived": true}, 200)
	a.json(alice, "POST", fmt.Sprintf("/api/v1/lists/%d/cards", doneL), map[string]any{"title": "Three"}, 201)
	a.json(alice, "POST", fmt.Sprintf("/api/v1/cards/%d/comments", oneID), map[string]any{"message": "nice"}, 201)
	a.json(alice, "PUT", fmt.Sprintf("/api/v1/cards/%d/members/%d", oneID, a.aliceID), nil, 200)
	body, ct := deckUpload(t, "file", "plan.txt", "the plan", nil)
	if code, raw, _ := a.do(alice, "POST", fmt.Sprintf("/api/v1/cards/%d/attachments", oneID), body, ct); code != 201 {
		t.Fatalf("upload: %d %s", code, raw)
	}

	// Bob imports it through the Deck API with Alice's login.
	a.setNextcloud(a.bobID, a.url, "alice@example.com", a.alice)
	bob := deckBearer(a.bob)
	var deckID int64
	for _, x := range arr(a.json(bob, "GET", "/api/v1/import/deck/boards", nil, 200)) {
		if obj(x)["title"] == "Source" {
			deckID = num(obj(x)["id"])
		}
	}
	if deckID != sid {
		t.Fatalf("source board not offered (%d)", deckID)
	}
	res := obj(a.json(bob, "POST", "/api/v1/import/deck", map[string]any{"board_ids": []int64{deckID}}, 200))
	if res["error"] != nil {
		t.Fatalf("import: %v", res)
	}
	ctx := context.Background()
	host := strings.TrimPrefix(a.url, "http://")
	b, err := a.st.BoardByExternal(ctx, a.bobID, "deck", fmt.Sprintf("%s:%d", host, sid))
	if err != nil || b.Title != "Source" || b.Color != "1f845a" || b.OwnerID != a.bobID {
		t.Fatalf("board: %+v %v", b, err)
	}
	srcLists, _ := a.st.Lists(ctx, sid, false)
	gotLists, _ := a.st.Lists(ctx, b.ID, false)
	if len(srcLists) != len(gotLists) {
		t.Fatalf("lists %d vs %d", len(srcLists), len(gotLists))
	}
	for i := range srcLists {
		if srcLists[i].Title != gotLists[i].Title || srcLists[i].IsDone != gotLists[i].IsDone {
			t.Fatalf("list %d: %+v vs %+v", i, srcLists[i], gotLists[i])
		}
	}
	labelTitles := func(id int64) []string {
		ls, _ := a.st.Labels(ctx, id)
		var out []string
		for _, l := range ls {
			out = append(out, l.Title+"/"+l.Color)
		}
		sort.Strings(out)
		return out
	}
	if fmt.Sprint(labelTitles(sid)) != fmt.Sprint(labelTitles(b.ID)) {
		t.Fatalf("labels %v vs %v", labelTitles(sid), labelTitles(b.ID))
	}
	cardsOf := func(id int64) map[string]store.Card {
		out := map[string]store.Card{}
		for _, arch := range []bool{false, true} {
			cs, _ := a.st.BoardCards(ctx, id, arch)
			for _, c := range cs {
				out[c.Title] = c
			}
		}
		return out
	}
	sc, gc := cardsOf(sid), cardsOf(b.ID)
	if len(sc) != 3 || len(gc) != 3 {
		t.Fatalf("cards %d vs %d", len(sc), len(gc))
	}
	for title, s := range sc {
		g := gc[title]
		sd, _ := parseTime(s.DueAt)
		gd, _ := parseTime(g.DueAt)
		if g.Description != s.Description || !sd.Equal(gd) || g.Done != s.Done || g.Archived != s.Archived || len(g.Labels) != len(s.Labels) {
			t.Fatalf("card %q: %+v vs %+v", title, s, g)
		}
	}
	got := gc["One"]
	if len(got.Members) != 1 || got.Members[0] != a.bobID {
		t.Fatalf("assignee should map to the importer: %v", got.Members)
	}
	cs, _ := a.st.Comments(ctx, got.ID)
	if len(cs) != 1 || cs[0].Message != "nice" || cs[0].UserID == nil || *cs[0].UserID != a.bobID {
		t.Fatalf("comment: %+v", cs)
	}
	atts, _ := a.st.Attachments(ctx, got.ID)
	if len(atts) != 1 {
		t.Fatalf("attachments: %+v", atts)
	}
	data, _ := os.ReadFile(a.s.cfg.FilesDir + "/" + atts[0].Stored)
	if string(data) != "the plan" || atts[0].Filename != "plan.txt" {
		t.Fatalf("attachment %q %q", atts[0].Filename, data)
	}
	// Bob's copy didn't share anything with Alice.
	if ms, _ := a.st.MemberIDs(ctx, b.ID); len(ms) != 1 {
		t.Fatalf("members: %v", ms)
	}
}
