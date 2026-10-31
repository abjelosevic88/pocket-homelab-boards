package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// ---- importing from Nextcloud Deck ----
//
// Boards reads a person's Deck boards through the Deck API with their own
// Nextcloud login (from Pocket Core's Nextcloud service, or typed into
// Settings). Every board, list, label, card and comment keeps where it came
// from ("<nextcloud host>:<deck id>"), so importing again updates what is
// there and adds what is new — never a second copy.

const deckImportMaxBytes = 500 << 20 // files per import

type ncClient struct {
	base, user, pass string
	host             string
	http             *http.Client
}

func newNCClient(base, user, pass string) (*ncClient, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || user == "" || pass == "" {
		return nil, errors.New("the Nextcloud address, user and app password are needed (Settings → Connected apps)")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, errors.New("that Nextcloud address doesn't look right")
	}
	return &ncClient{base: base, user: user, pass: pass, host: u.Host, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (c *ncClient) req(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "PocketBoards/"+Version)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		res.Body.Close()
		switch res.StatusCode {
		case http.StatusUnauthorized:
			return nil, errors.New("Nextcloud did not accept the user and app password")
		case http.StatusNotFound:
			return nil, fmt.Errorf("not found on Nextcloud (%s) — is the Deck app installed?", path)
		}
		return nil, fmt.Errorf("Nextcloud answered HTTP %d for %s", res.StatusCode, path)
	}
	return res, nil
}

func (c *ncClient) get(ctx context.Context, path string, v any) error {
	res, err := c.req(ctx, path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 256<<20)).Decode(v); err != nil {
		return fmt.Errorf("unexpected answer from Nextcloud for %s: %w", path, err)
	}
	return nil
}

const deckAPI = "/index.php/apps/deck/api/v1.0"

// Deck's JSON, read leniently (versions differ).
type ncUser struct {
	UID         string `json:"uid"`
	PrimaryKey  string `json:"primaryKey"`
	Displayname string `json:"displayname"`
}

func (u ncUser) id() string { return firstNonEmpty(u.UID, u.PrimaryKey) }

type ncLabel struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}

type ncBoard struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Color     string    `json:"color"`
	Archived  bool      `json:"archived"`
	DeletedAt int64     `json:"deletedAt"`
	Owner     ncUserRaw `json:"owner"`
	Labels    []ncLabel `json:"labels"`
	Users     []ncUser  `json:"users"`
	ACL       []struct {
		Type           int    `json:"type"`
		Participant    ncUser `json:"participant"`
		PermissionEdit bool   `json:"permissionEdit"`
	} `json:"acl"`
}

// ncUserRaw is a user given as an object or as a plain uid.
type ncUserRaw struct{ ncUser }

func (u *ncUserRaw) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		u.UID = s
		return nil
	}
	return json.Unmarshal(b, &u.ncUser)
}

type ncCard struct {
	ID            int64           `json:"id"`
	Title         string          `json:"title"`
	Description   *string         `json:"description"`
	StackID       int64           `json:"stackId"`
	Order         int             `json:"order"`
	Archived      bool            `json:"archived"`
	Duedate       *string         `json:"duedate"`
	Done          json.RawMessage `json:"done"`
	DeletedAt     int64           `json:"deletedAt"`
	CreatedAt     int64           `json:"createdAt"`
	LastModified  int64           `json:"lastModified"`
	Labels        []ncLabel       `json:"labels"`
	AssignedUsers []struct {
		Participant ncUser `json:"participant"`
	} `json:"assignedUsers"`
}

type ncStack struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Order     int      `json:"order"`
	DeletedAt int64    `json:"deletedAt"`
	Cards     []ncCard `json:"cards"`
}

type ncComment struct {
	ID               int64  `json:"id"`
	Message          string `json:"message"`
	ActorID          string `json:"actorId"`
	ActorDisplayName string `json:"actorDisplayName"`
	CreationDateTime string `json:"creationDateTime"`
}

type ncAttachment struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	Data         string `json:"data"`
	DeletedAt    int64  `json:"deletedAt"`
	ExtendedData struct {
		Filesize int64  `json:"filesize"`
		Mimetype string `json:"mimetype"`
	} `json:"extendedData"`
}

// ncFor is the person's Nextcloud connection.
func (s *Server) ncFor(ctx context.Context, uid int64) (*ncClient, error) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	switch s.connMode(st.NextcloudMode, st.NextcloudPassword) {
	case modeOff:
		return nil, errors.New("Nextcloud is switched off in Settings → Connected apps")
	case modeDirect:
		return newNCClient(st.NextcloudURL, st.NextcloudUser, st.NextcloudPassword)
	}
	if !s.core.Configured() {
		return nil, errors.New("there is no Pocket Core on this box; add your Nextcloud address, user and app password in Settings → Connected apps")
	}
	u, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	cred, err := s.core.ServiceFor(ctx, u.Email, "nextcloud")
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			return nil, fmt.Errorf("Nextcloud through Pocket Core: %s", le.Message)
		}
		return nil, fmt.Errorf("Nextcloud through Pocket Core: %w", err)
	}
	user := cred.Fields["USERNAME"]
	pass := firstNonEmpty(cred.Fields["APP_PASSWORD"], firstNonEmpty(cred.Fields["PASSWORD"], cred.Key))
	if cred.URL == "" || user == "" || pass == "" {
		return nil, errors.New("Pocket Core has no Nextcloud login for you (address, username and app password)")
	}
	return newNCClient(cred.URL, user, pass)
}

func (s *Server) testNextcloud(ctx context.Context, uid int64, url, user, pass string, typed bool) (string, error) {
	var c *ncClient
	var err error
	if typed {
		c, err = newNCClient(url, user, pass)
	} else {
		c, err = s.ncFor(ctx, uid)
	}
	if err != nil {
		return "", err
	}
	var status struct {
		Productname   string `json:"productname"`
		Versionstring string `json:"versionstring"`
	}
	if err := c.get(ctx, "/status.php", &status); err != nil {
		return "", err
	}
	var boards []ncBoard
	if err := c.get(ctx, deckAPI+"/boards", &boards); err != nil {
		return "", err
	}
	n := 0
	for _, b := range boards {
		if b.DeletedAt == 0 {
			n++
		}
	}
	return fmt.Sprintf("%s %s, Deck: %d boards", firstNonEmpty(status.Productname, "Nextcloud"), status.Versionstring, n), nil
}

func (s *Server) handleDeckImportBoards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.ncFor(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var boards []ncBoard
	if err := c.get(ctx, deckAPI+"/boards", &boards); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := []map[string]any{}
	for _, b := range boards {
		if b.DeletedAt != 0 {
			continue
		}
		var imported any
		if lb, err := s.store.BoardByExternal(ctx, uid, "deck", deckSafeID(c.host, b.ID)); err == nil && lb.DeletedAt == "" {
			imported = lb.ID
		}
		owner := b.Owner.id()
		out = append(out, map[string]any{"id": b.ID, "title": b.Title, "color": b.Color, "archived": b.Archived, "imported_board_id": imported,
			"owner": firstNonEmpty(b.Owner.Displayname, owner), "shared": owner != "" && !strings.EqualFold(owner, c.user)})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- the import job ----

type deckJob struct {
	Running  bool           `json:"running"`
	Done     bool           `json:"done"`
	Error    string         `json:"error,omitempty"`
	Progress string         `json:"progress"`
	Counts   map[string]int `json:"counts"`
	Started  string         `json:"started_at"`
	finished chan struct{}
}

type deckJobKey struct {
	s   *Server
	uid int64
}

var deckJobs = struct {
	sync.Mutex
	m map[deckJobKey]*deckJob
}{m: map[deckJobKey]*deckJob{}}

func (j *deckJob) snapshot() map[string]any {
	deckJobs.Lock()
	defer deckJobs.Unlock()
	counts := map[string]int{}
	for k, v := range j.Counts {
		counts[k] = v
	}
	out := map[string]any{"running": j.Running, "done": j.Done, "progress": j.Progress, "counts": counts, "started_at": j.Started}
	if j.Error != "" {
		out["error"] = j.Error
	}
	for k, v := range counts {
		out[k] = v
	}
	return out
}

func (j *deckJob) set(f func()) {
	deckJobs.Lock()
	f()
	deckJobs.Unlock()
}

// handleImportDeck starts the import in the background and waits up to 90
// seconds for it: small imports answer with their counts, big ones answer
// {started: true} and report through GET /api/v1/import/deck/status.
func (s *Server) handleImportDeck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		BoardIDs    []int64 `json:"board_ids"`
		Comments    *bool   `json:"comments"`
		Attachments *bool   `json:"attachments"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.BoardIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "pick at least one board")
		return
	}
	c, err := s.ncFor(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	opts := deckImportOpts{comments: in.Comments == nil || *in.Comments, attachments: in.Attachments == nil || *in.Attachments}
	key := deckJobKey{s, uid}
	deckJobs.Lock()
	if j := deckJobs.m[key]; j != nil && j.Running {
		deckJobs.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "an import is already running", "running": true})
		return
	}
	job := &deckJob{Running: true, Counts: map[string]int{}, Started: store.Now(), Progress: "starting", finished: make(chan struct{})}
	deckJobs.m[key] = job
	deckJobs.Unlock()

	u := userFrom(ctx)
	go func() {
		bctx, cancel := context.WithTimeout(context.WithValue(context.Background(), userKey, u), 30*time.Minute)
		defer cancel()
		err := s.importDeck(bctx, c, uid, in.BoardIDs, opts, job)
		job.set(func() {
			job.Running, job.Done, job.Progress = false, true, "finished"
			if err != nil {
				job.Error = err.Error()
			}
		})
		close(job.finished)
	}()
	select {
	case <-job.finished:
		snap := job.snapshot()
		if e, ok := snap["error"]; ok && len(job.Counts) == 0 {
			writeErr(w, http.StatusBadGateway, e.(string))
			return
		}
		writeJSON(w, http.StatusOK, snap)
	case <-time.After(90 * time.Second):
		snap := job.snapshot()
		snap["started"] = true
		writeJSON(w, http.StatusAccepted, snap)
	case <-ctx.Done():
	}
}

func (s *Server) handleDeckImportStatus(w http.ResponseWriter, r *http.Request) {
	deckJobs.Lock()
	j := deckJobs.m[deckJobKey{s, actorID(r.Context())}]
	deckJobs.Unlock()
	if j == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "done": false})
		return
	}
	writeJSON(w, http.StatusOK, j.snapshot())
}

type deckImportOpts struct{ comments, attachments bool }

// deckDoneWords mark a Deck stack as the done list.
var deckDoneWords = map[string]bool{"done": true, "finished": true, "complete": true, "completed": true, "gotovo": true, "završeno": true, "zavrseno": true,
	"urađeno": true, "uradjeno": true, "erledigt": true, "fertig": true, "terminé": true, "termine": true, "hecho": true, "terminado": true, "klaar": true,
	"fatto": true, "concluído": true, "concluido": true, "готово": true, "выполнено": true, "завршено": true}

func looksDone(title string) bool {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r > 127 && r != '✓' && r != '✅')
	})
	if len(words) == 0 || len(words) > 3 {
		return false
	}
	for _, w := range words {
		if deckDoneWords[w] {
			return true
		}
	}
	return false
}

func deckDoneAt(raw json.RawMessage, fallback int64) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, ok := parseTime(s); ok {
			return t.UTC().Format(time.RFC3339Nano)
		}
	}
	if fallback > 0 {
		return time.Unix(fallback, 0).UTC().Format(time.RFC3339Nano)
	}
	return store.Now()
}

func ncTime(v *string) string {
	if v == nil {
		return ""
	}
	t, ok := parseTime(*v)
	if !ok {
		return ""
	}
	return t.In(time.Local).Format(time.RFC3339)
}

func (s *Server) importDeck(ctx context.Context, c *ncClient, uid int64, ids []int64, opts deckImportOpts, job *deckJob) error {
	budget := int64(deckImportMaxBytes)
	var firstErr error
	for i, id := range ids {
		job.set(func() { job.Progress = fmt.Sprintf("board %d of %d", i+1, len(ids)) })
		if err := s.importDeckBoard(ctx, c, uid, id, opts, job, &budget); err != nil {
			s.log.Warn("deck import", "board", id, "err", err)
			job.set(func() { job.Counts["failed_boards"]++ })
			if firstErr == nil {
				firstErr = fmt.Errorf("board %d: %w", id, err)
			}
		}
	}
	return firstErr
}

func (s *Server) importDeckBoard(ctx context.Context, c *ncClient, uid, id int64, opts deckImportOpts, job *deckJob, budget *int64) error {
	count := func(k string, n int) { job.set(func() { job.Counts[k] += n }) }
	var nb ncBoard
	if err := c.get(ctx, fmt.Sprintf("%s/boards/%d", deckAPI, id), &nb); err != nil {
		return err
	}
	var stacks, archived []ncStack
	if err := c.get(ctx, fmt.Sprintf("%s/boards/%d/stacks", deckAPI, id), &stacks); err != nil {
		return err
	}
	_ = c.get(ctx, fmt.Sprintf("%s/boards/%d/stacks/archived", deckAPI, id), &archived)

	// The board.
	ext := deckSafeID(c.host, nb.ID)
	color := cleanColor(nb.Color)
	if !hexColor.MatchString(color) {
		color = "0c66e4"
	}
	b, err := s.store.BoardByExternal(ctx, uid, "deck", ext)
	if err != nil {
		nbd := store.Board{Title: firstNonEmpty(strings.TrimSpace(nb.Title), "Deck board"), Color: color, Archived: nb.Archived, Source: "deck", ExternalID: ext}
		if err := s.store.CreateBoard(ctx, uid, &nbd); err != nil {
			return err
		}
		b = &nbd
		count("boards", 1)
	} else {
		if b.DeletedAt != "" {
			if err := s.store.TrashBoard(ctx, b.ID, false); err != nil {
				return err
			}
		}
		b.Title, b.Color, b.Archived = firstNonEmpty(strings.TrimSpace(nb.Title), b.Title), color, nb.Archived
		if err := s.store.UpdateBoard(ctx, b); err != nil {
			return err
		}
		count("updated_boards", 1)
	}

	// People: the Nextcloud account being imported is the person importing;
	// others are matched to accounts on this box and share the board.
	people := map[string]int64{}
	edit := map[string]bool{}
	for _, a := range nb.ACL {
		if a.Type == 0 {
			edit[strings.ToLower(a.Participant.id())] = a.PermissionEdit
		}
	}
	members, _ := s.store.MemberIDs(ctx, b.ID)
	resolve := func(u ncUser) int64 {
		key := strings.ToLower(u.id())
		if key == "" {
			return 0
		}
		if id, ok := people[key]; ok {
			return id
		}
		local := s.localForNC(ctx, uid, c.user, u)
		people[key] = local
		if local > 0 && !containsID(members, local) {
			role := store.RoleEditor
			if e, ok := edit[key]; ok && !e {
				role = store.RoleViewer
			}
			if s.store.SetMember(ctx, b.ID, local, role) == nil {
				members = append(members, local)
				count("members", 1)
			}
		}
		return local
	}
	for _, u := range nb.Users {
		resolve(u)
	}

	// Labels.
	have, _ := s.store.Labels(ctx, b.ID)
	labelIDs := map[int64]int64{}
	for _, l := range nb.Labels {
		lext := deckSafeID(c.host, l.ID)
		lc := cleanColor(l.Color)
		if !hexColor.MatchString(lc) {
			lc = "8590a2"
		}
		var found *store.Label
		for i := range have {
			if have[i].ExternalID == lext {
				found = &have[i]
			}
		}
		if found == nil {
			nl := store.Label{BoardID: b.ID, Title: l.Title, Color: lc, ExternalID: lext}
			if err := s.store.CreateLabel(ctx, &nl); err != nil {
				return err
			}
			labelIDs[l.ID] = nl.ID
			count("labels", 1)
			continue
		}
		if found.Title != l.Title || found.Color != lc {
			found.Title, found.Color = l.Title, lc
			_ = s.store.UpdateLabel(ctx, found)
		}
		labelIDs[l.ID] = found.ID
	}

	// Stacks and their cards (archived cards come per stack too).
	byStack := map[int64]*ncStack{}
	var order []*ncStack
	for i := range stacks {
		if stacks[i].DeletedAt != 0 {
			continue
		}
		byStack[stacks[i].ID] = &stacks[i]
		order = append(order, &stacks[i])
	}
	for _, as := range archived {
		if st, ok := byStack[as.ID]; ok {
			st.Cards = append(st.Cards, as.Cards...)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].Order < order[j].Order })
	for _, st := range order {
		sext := deckSafeID(c.host, st.ID)
		l, err := s.store.ListByExternal(ctx, b.ID, sext)
		if err != nil {
			nl := store.List{BoardID: b.ID, Title: firstNonEmpty(strings.TrimSpace(st.Title), "List"), IsDone: looksDone(st.Title), ExternalID: sext}
			if err := s.store.CreateList(ctx, &nl, -1); err != nil {
				return err
			}
			l = &nl
			count("lists", 1)
		} else {
			if l.DeletedAt != "" {
				_ = s.store.TrashList(ctx, l.ID, false)
			}
			if t := strings.TrimSpace(st.Title); t != "" && t != l.Title {
				l.Title = t
				_ = s.store.UpdateList(ctx, l)
			}
		}
		cards := st.Cards
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].Order < cards[j].Order })
		for _, nc := range cards {
			if nc.DeletedAt != 0 {
				continue
			}
			card, err := s.importDeckCard(ctx, uid, b, l, c.host, nc, labelIDs, resolve, members, count)
			if err != nil {
				return err
			}
			if card == nil {
				continue
			}
			if opts.comments {
				if err := s.importDeckComments(ctx, c, uid, card, nc.ID, resolve, count); err != nil {
					s.log.Warn("deck import: comments", "card", nc.ID, "err", err)
					count("comment_errors", 1)
				}
			}
			if opts.attachments {
				if err := s.importDeckAttachments(ctx, c, card, nb.ID, st.ID, nc.ID, budget, count); err != nil {
					s.log.Warn("deck import: attachments", "card", nc.ID, "err", err)
					count("attachment_errors", 1)
				}
			}
		}
	}
	s.changed(ctx, b, nil, "board_imported", map[string]any{"title": b.Title, "from": "Nextcloud Deck"})
	return nil
}

// localForNC maps a Nextcloud user to an account here: the Nextcloud login
// being imported is the importer; else the same email, or the same name.
func (s *Server) localForNC(ctx context.Context, importer int64, ncLogin string, u ncUser) int64 {
	id := strings.ToLower(u.id())
	if id == strings.ToLower(ncLogin) {
		return importer
	}
	if lu, err := s.store.GetUserByEmail(ctx, id); err == nil {
		return lu.ID
	}
	users, _ := s.store.ListUsers(ctx)
	for _, lu := range users {
		if (u.Displayname != "" && strings.EqualFold(lu.Name, u.Displayname)) || strings.EqualFold(strings.SplitN(lu.Email, "@", 2)[0], id) {
			return lu.ID
		}
	}
	return 0
}

func (s *Server) importDeckCard(ctx context.Context, uid int64, b *store.Board, l *store.List, host string, nc ncCard, labelIDs map[int64]int64,
	resolve func(ncUser) int64, members []int64, count func(string, int)) (*store.Card, error) {
	ext := deckSafeID(host, nc.ID)
	desc := ""
	if nc.Description != nil {
		desc = *nc.Description
	}
	done := deckDoneAt(nc.Done, nc.LastModified)
	if done == "" && l.IsDone {
		done = time.Unix(max(nc.LastModified, 1), 0).UTC().Format(time.RFC3339Nano)
	}
	var labels []int64
	for _, x := range nc.Labels {
		if id, ok := labelIDs[x.ID]; ok {
			labels = append(labels, id)
		}
	}
	var assignees []int64
	for _, a := range nc.AssignedUsers {
		if id := resolve(a.Participant); id > 0 && !containsID(assignees, id) {
			assignees = append(assignees, id)
		}
	}
	existing, err := s.store.CardByExternal(ctx, b.ID, "deck", ext)
	if err != nil {
		card := store.Card{BoardID: b.ID, ListID: l.ID, Title: firstNonEmpty(strings.TrimSpace(nc.Title), "Card"), Description: desc, DueAt: ncTime(nc.Duedate),
			DoneAt: done, Archived: nc.Archived, Source: "deck", ExternalID: ext, Labels: labels, Members: assignees}
		if nc.CreatedAt > 0 {
			card.CreatedAt = time.Unix(nc.CreatedAt, 0).UTC().Format(time.RFC3339Nano)
		}
		if err := s.store.CreateCard(ctx, uid, &card, -1); err != nil {
			return nil, err
		}
		count("cards", 1)
		return &card, nil
	}
	if existing.DeletedAt != "" {
		count("skipped", 1) // deleted here on purpose: leave it in the bin
		return nil, nil
	}
	existing.Title = firstNonEmpty(strings.TrimSpace(nc.Title), existing.Title)
	existing.Description, existing.DueAt, existing.DoneAt, existing.Archived = desc, ncTime(nc.Duedate), done, nc.Archived
	if err := s.store.UpdateCard(ctx, uid, existing); err != nil {
		return nil, err
	}
	if existing.ListID != l.ID {
		if err := s.store.MoveCard(ctx, existing.ID, l.ID, -1); err != nil {
			return nil, err
		}
	}
	for _, id := range labels {
		if !containsID(existing.Labels, id) {
			_ = s.store.SetCardLabel(ctx, existing, id, true)
		}
	}
	for _, id := range existing.Labels {
		if !containsID(labels, id) {
			_ = s.store.SetCardLabel(ctx, existing, id, false)
		}
	}
	for _, id := range assignees {
		_, _ = s.store.SetCardMember(ctx, existing, id, true)
	}
	count("updated_cards", 1)
	return existing, nil
}

func (s *Server) importDeckComments(ctx context.Context, c *ncClient, uid int64, card *store.Card, ncCardID int64, resolve func(ncUser) int64, count func(string, int)) error {
	for offset := 0; offset < 5000; offset += 100 {
		var out struct {
			OCS struct {
				Data []ncComment `json:"data"`
			} `json:"ocs"`
		}
		if err := c.get(ctx, fmt.Sprintf("/ocs/v2.php/apps/deck/api/v1.0/cards/%d/comments?limit=100&offset=%d&format=json", ncCardID, offset), &out); err != nil {
			return err
		}
		for _, cm := range out.OCS.Data {
			ext := deckSafeID(c.host, cm.ID)
			if s.store.CommentExists(ctx, card.ID, "deck", ext) {
				continue
			}
			author := resolve(ncUser{UID: cm.ActorID, Displayname: cm.ActorDisplayName})
			msg := cm.Message
			if author == 0 {
				author = uid
				msg = "**" + firstNonEmpty(cm.ActorDisplayName, cm.ActorID) + ":** " + msg
			}
			created := ""
			if t, ok := parseTime(cm.CreationDateTime); ok {
				created = t.UTC().Format(time.RFC3339Nano)
			}
			ncm := store.Comment{CardID: card.ID, Message: msg, Source: "deck", ExternalID: ext, CreatedAt: created}
			if err := s.store.CreateComment(ctx, author, &ncm); err != nil {
				return err
			}
			count("comments", 1)
		}
		if len(out.OCS.Data) < 100 {
			return nil
		}
	}
	return nil
}

func (s *Server) importDeckAttachments(ctx context.Context, c *ncClient, card *store.Card, boardID, stackID, ncCardID int64, budget *int64, count func(string, int)) error {
	base := fmt.Sprintf("/index.php/apps/deck/api/%%s/boards/%d/stacks/%d/cards/%d/attachments", boardID, stackID, ncCardID)
	version := "v1.1"
	var list []ncAttachment
	if err := c.get(ctx, fmt.Sprintf(base, version), &list); err != nil {
		version = "v1.0"
		if err := c.get(ctx, fmt.Sprintf(base, version), &list); err != nil {
			return err
		}
	}
	have, _ := s.store.Attachments(ctx, card.ID)
	for _, a := range list {
		if a.DeletedAt != 0 || a.Data == "" {
			continue
		}
		name := a.Data
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		dup := false
		for _, h := range have {
			dup = dup || (h.Filename == name && (a.ExtendedData.Filesize == 0 || h.Size == a.ExtendedData.Filesize))
		}
		if dup {
			continue
		}
		if a.ExtendedData.Filesize > *budget {
			count("attachments_skipped", 1)
			continue
		}
		path := fmt.Sprintf(base, version) + fmt.Sprintf("/%d", a.ID)
		if version == "v1.1" {
			path = fmt.Sprintf(base, version) + fmt.Sprintf("/%s/%d", firstNonEmpty(a.Type, "deck_file"), a.ID)
		}
		res, err := c.req(ctx, path)
		if err != nil {
			return err
		}
		lr := &io.LimitedReader{R: res.Body, N: *budget + 1}
		att, err := s.saveAttachment(ctx, card, name, firstNonEmpty(a.ExtendedData.Mimetype, res.Header.Get("Content-Type")), lr)
		res.Body.Close()
		if err != nil {
			return err
		}
		if att.Size > *budget {
			_ = s.store.TrashAttachment(ctx, att.ID, true)
			count("attachments_skipped", 1)
			*budget = 0
			continue
		}
		*budget -= att.Size
		have = append(have, *att)
		count("attachments", 1)
	}
	return nil
}
