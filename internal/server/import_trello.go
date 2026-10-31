package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// ---- importing a Trello board export ----
//
// Trello → Board menu → Print, export and share → Export as JSON. Lists,
// cards, labels, checklists (as task lines in the description, like Deck)
// and comments come over; closed lists are left out. Everything keeps its
// Trello id, so importing the same export again adds only what is new.

type trelloExport struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Desc  string `json:"desc"`
	Prefs struct {
		BackgroundColor string `json:"backgroundColor"`
		Background      string `json:"background"`
	} `json:"prefs"`
	Labels []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"labels"`
	Lists []struct {
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		Closed bool    `json:"closed"`
		Pos    float64 `json:"pos"`
	} `json:"lists"`
	Cards []struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Desc             string   `json:"desc"`
		Closed           bool     `json:"closed"`
		IDList           string   `json:"idList"`
		Pos              float64  `json:"pos"`
		Due              *string  `json:"due"`
		Start            *string  `json:"start"`
		DueComplete      bool     `json:"dueComplete"`
		IDLabels         []string `json:"idLabels"`
		DateLastActivity string   `json:"dateLastActivity"`
		Cover            struct {
			Color *string `json:"color"`
		} `json:"cover"`
		Attachments []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"attachments"`
	} `json:"cards"`
	Checklists []struct {
		ID         string  `json:"id"`
		IDCard     string  `json:"idCard"`
		Name       string  `json:"name"`
		Pos        float64 `json:"pos"`
		CheckItems []struct {
			Name  string  `json:"name"`
			State string  `json:"state"`
			Pos   float64 `json:"pos"`
		} `json:"checkItems"`
	} `json:"checklists"`
	Actions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Date string `json:"date"`
		Data struct {
			Text string `json:"text"`
			Card struct {
				ID string `json:"id"`
			} `json:"card"`
		} `json:"data"`
		MemberCreator struct {
			FullName string `json:"fullName"`
			Username string `json:"username"`
		} `json:"memberCreator"`
	} `json:"actions"`
}

// trelloColors are Trello's colour names in our palette.
var trelloColors = map[string]string{"green": "4bce97", "yellow": "f5cd47", "orange": "fea362", "red": "f87168", "purple": "9f8fef",
	"blue": "579dff", "sky": "6cc3e0", "lime": "94c748", "pink": "e774bb", "black": "8590a2", "grey": "8590a2", "gray": "8590a2"}

func trelloColor(name, fallback string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if c := cleanColor(name); hexColor.MatchString(c) {
		return c
	}
	if i := strings.Index(name, "_"); i > 0 {
		name = name[:i] // green_dark, green_light → green
	}
	if c, ok := trelloColors[name]; ok {
		return c
	}
	return fallback
}

func (s *Server) handleImportTrello(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "send the Trello export as the \"file\" field")
			return
		}
		f, _, ferr := r.FormFile("file")
		if ferr != nil {
			writeErr(w, http.StatusBadRequest, "no file")
			return
		}
		defer f.Close()
		raw, err = io.ReadAll(f)
	} else {
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "couldn't read the file")
		return
	}
	var tb trelloExport
	if err := json.Unmarshal(raw, &tb); err != nil || tb.ID == "" || (tb.Lists == nil && tb.Cards == nil) {
		writeErr(w, http.StatusBadRequest, "this isn't a Trello board export (Trello → board menu → Print, export and share → Export as JSON)")
		return
	}
	counts, err := s.importTrello(r.Context(), actorID(r.Context()), &tb)
	if err != nil {
		s.fail(w, err, "Trello import")
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

func trelloTime(v *string) string {
	if v == nil {
		return ""
	}
	t, ok := parseTime(*v)
	if !ok {
		return ""
	}
	return t.In(time.Local).Format(time.RFC3339)
}

func (s *Server) importTrello(ctx context.Context, uid int64, tb *trelloExport) (map[string]int, error) {
	counts := map[string]int{"boards": 0, "lists": 0, "labels": 0, "cards": 0, "comments": 0, "skipped": 0}
	ext := "trello:" + tb.ID
	b, err := s.store.BoardByExternal(ctx, uid, "trello", ext)
	if err != nil {
		nb := store.Board{Title: firstNonEmpty(strings.TrimSpace(tb.Name), "Trello board"), Description: tb.Desc,
			Color: trelloColor(firstNonEmpty(tb.Prefs.BackgroundColor, tb.Prefs.Background), "0c66e4"), Source: "trello", ExternalID: ext}
		if err := s.store.CreateBoard(ctx, uid, &nb); err != nil {
			return nil, err
		}
		b = &nb
		counts["boards"]++
	} else if b.DeletedAt != "" {
		if err := s.store.TrashBoard(ctx, b.ID, false); err != nil {
			return nil, err
		}
	}

	have, _ := s.store.Labels(ctx, b.ID)
	labelIDs := map[string]int64{}
	for _, l := range tb.Labels {
		lext := "trello:" + l.ID
		found := int64(0)
		for _, h := range have {
			if h.ExternalID == lext {
				found = h.ID
			}
		}
		if found == 0 {
			nl := store.Label{BoardID: b.ID, Title: l.Name, Color: trelloColor(l.Color, "8590a2"), ExternalID: lext}
			if err := s.store.CreateLabel(ctx, &nl); err != nil {
				return nil, err
			}
			found = nl.ID
			counts["labels"]++
		}
		labelIDs[l.ID] = found
	}

	lists := tb.Lists
	sort.SliceStable(lists, func(i, j int) bool { return lists[i].Pos < lists[j].Pos })
	listIDs := map[string]*store.List{}
	for _, tl := range lists {
		if tl.Closed {
			continue
		}
		lext := "trello:" + tl.ID
		l, err := s.store.ListByExternal(ctx, b.ID, lext)
		if err != nil {
			nl := store.List{BoardID: b.ID, Title: firstNonEmpty(strings.TrimSpace(tl.Name), "List"), IsDone: looksDone(tl.Name), ExternalID: lext}
			if err := s.store.CreateList(ctx, &nl, -1); err != nil {
				return nil, err
			}
			l = &nl
			counts["lists"]++
		}
		listIDs[tl.ID] = l
	}

	// Checklists, attachments and comments by card.
	checklists := map[string][]int{}
	for i, cl := range tb.Checklists {
		checklists[cl.IDCard] = append(checklists[cl.IDCard], i)
	}
	type comment struct{ id, text, who, date string }
	comments := map[string][]comment{}
	me, _ := s.store.GetUser(ctx, uid)
	for _, a := range tb.Actions {
		if a.Type != "commentCard" || a.Data.Card.ID == "" || strings.TrimSpace(a.Data.Text) == "" {
			continue
		}
		comments[a.Data.Card.ID] = append(comments[a.Data.Card.ID], comment{a.ID, a.Data.Text, firstNonEmpty(a.MemberCreator.FullName, a.MemberCreator.Username), a.Date})
	}

	cards := tb.Cards
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Pos < cards[j].Pos })
	for _, tc := range cards {
		l, ok := listIDs[tc.IDList]
		if !ok {
			counts["skipped"]++ // on a closed list
			continue
		}
		cext := "trello:" + tc.ID
		card, err := s.store.CardByExternal(ctx, b.ID, "trello", cext)
		if err == nil {
			counts["skipped"]++
		} else {
			desc := strings.TrimRight(tc.Desc, "\n")
			idx := checklists[tc.ID]
			sort.SliceStable(idx, func(i, j int) bool { return tb.Checklists[idx[i]].Pos < tb.Checklists[idx[j]].Pos })
			for _, i := range idx {
				cl := tb.Checklists[i]
				items := cl.CheckItems
				sort.SliceStable(items, func(i, j int) bool { return items[i].Pos < items[j].Pos })
				var sb strings.Builder
				fmt.Fprintf(&sb, "### %s\n", firstNonEmpty(strings.TrimSpace(cl.Name), "Checklist"))
				for _, it := range items {
					mark := " "
					if it.State == "complete" {
						mark = "x"
					}
					fmt.Fprintf(&sb, "- [%s] %s\n", mark, strings.TrimSpace(it.Name))
				}
				desc = strings.TrimLeft(desc+"\n\n"+strings.TrimRight(sb.String(), "\n"), "\n")
			}
			if len(tc.Attachments) > 0 {
				var sb strings.Builder
				sb.WriteString("### Links\n")
				for _, a := range tc.Attachments {
					if a.URL != "" {
						fmt.Fprintf(&sb, "- [%s](%s)\n", firstNonEmpty(a.Name, a.URL), a.URL)
					}
				}
				desc = strings.TrimLeft(desc+"\n\n"+strings.TrimRight(sb.String(), "\n"), "\n")
			}
			nc := store.Card{BoardID: b.ID, ListID: l.ID, Title: firstNonEmpty(strings.TrimSpace(tc.Name), "Card"), Description: desc,
				DueAt: trelloTime(tc.Due), StartAt: trelloTime(tc.Start), Archived: tc.Closed, Source: "trello", ExternalID: cext}
			if tc.Cover.Color != nil {
				nc.CoverColor = trelloColor(*tc.Cover.Color, "")
			}
			if tc.DueComplete || l.IsDone {
				if t, ok := parseTime(tc.DateLastActivity); ok {
					nc.DoneAt = t.UTC().Format(time.RFC3339Nano)
				} else {
					nc.DoneAt = store.Now()
				}
			}
			for _, id := range tc.IDLabels {
				if lid, ok := labelIDs[id]; ok {
					nc.Labels = append(nc.Labels, lid)
				}
			}
			if err := s.store.CreateCard(ctx, uid, &nc, -1); err != nil {
				return nil, err
			}
			card = &nc
			counts["cards"]++
		}
		// Trello lists comments newest first.
		cs := comments[tc.ID]
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].date < cs[j].date })
		for _, cm := range cs {
			cext := "trello:" + cm.id
			if s.store.CommentExists(ctx, card.ID, "trello", cext) {
				continue
			}
			msg := cm.text
			if me == nil || !strings.EqualFold(cm.who, me.Name) {
				msg = "**" + firstNonEmpty(cm.who, "Someone") + ":** " + msg
			}
			created := ""
			if t, ok := parseTime(cm.date); ok {
				created = t.UTC().Format(time.RFC3339Nano)
			}
			ncm := store.Comment{CardID: card.ID, Message: msg, Source: "trello", ExternalID: cext, CreatedAt: created}
			if err := s.store.CreateComment(ctx, uid, &ncm); err != nil {
				return nil, err
			}
			counts["comments"]++
		}
	}
	s.changed(ctx, b, nil, "board_imported", map[string]any{"title": b.Title, "from": "Trello"})
	return counts, nil
}
