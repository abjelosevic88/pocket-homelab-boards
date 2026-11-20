package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// moveApp starts Boards behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (as members).
func moveApp(t *testing.T) *bapp {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "boards:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"boards","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newBoardsAppWith(t, coreSrv.URL)
}

func (a *bapp) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (a *bapp) moveImport(tok, path string, data []byte, multi bool) map[string]any {
	a.t.Helper()
	ctype, body := "application/json", data
	if multi {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "upload.json")
		_, _ = fw.Write(data)
		_ = mw.Close()
		ctype, body = mw.FormDataContentType(), buf.Bytes()
	}
	code, out := a.raw(tok, "POST", path, ctype, body)
	if code != 200 {
		a.t.Fatalf("POST %s → %d %s", path, code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveApp(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "POST", "/api/v1/import/trello", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/boards", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/boards", 403},
		{imp, "POST", "/api/v1/import/deck", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 2 || p.Imports[1].Form != "file" {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's boards to Bob: each import adds once, and nothing of
// one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveApp(t)
	d := a.newBoard(a.alice, "Garden plan")
	a.newCard(a.alice, d.Lists[0].ID, map[string]any{"title": "Sow tomatoes"})

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "Sow tomatoes") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, csv := a.raw("pcx_alice_export", "GET", "/api/v1/export.csv", "", nil); !strings.Contains(string(csv), "Sow tomatoes") {
		t.Fatalf("alice's csv: %s", csv)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "Garden plan") {
		t.Fatalf("bob's export holds alice's board: %s", bob)
	}

	first := a.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	second := a.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	if num(first["boards"]) != 1 || num(first["cards"]) != 1 || num(second["boards"])+num(second["lists"])+num(second["cards"])+num(second["labels"]) != 0 {
		t.Fatalf("imports: %v then %v", first, second)
	}

	trello, err := os.ReadFile("testdata/trello.json")
	if err != nil {
		t.Fatal(err)
	}
	t1 := a.moveImport("pcx_bob_import", "/api/v1/import/trello", trello, true)
	t2 := a.moveImport("pcx_bob_import", "/api/v1/import/trello", trello, true)
	if num(t1["boards"]) != 1 || num(t1["cards"]) == 0 || num(t2["boards"])+num(t2["lists"])+num(t2["cards"])+num(t2["comments"]) != 0 {
		t.Fatalf("trello imports: %v then %v", t1, t2)
	}

	var bobBoards, aliceBoards []store.Board
	a.want(200, a.bob, "GET", "/api/v1/boards", nil, &bobBoards)
	a.want(200, a.alice, "GET", "/api/v1/boards", nil, &aliceBoards)
	if len(bobBoards) != 2 || len(aliceBoards) != 1 {
		t.Fatalf("boards: bob %d, alice %d", len(bobBoards), len(aliceBoards))
	}
}
