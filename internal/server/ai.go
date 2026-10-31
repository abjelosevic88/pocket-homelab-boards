package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// aiFor is the person's AI: Pocket AI through Core, their own
// OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 150*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 150*time.Second), "openai", ""
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

const aiVoice = `You help someone organise work on a Trello-style board. Be concrete and practical. Write in the same language as the person's text. Short items in plain words, no numbering, no emojis inside items, no filler.`

// handleAIChecklist suggests the steps for a card (nothing is saved).
func (s *Server) handleAIChecklist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		CardID int64 `json:"card_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, b := s.cardFor(w, r, in.CardID, false, false)
	if c == nil {
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, _, why := s.aiFor(ctx, actorID(ctx), st)
	if cl == nil {
		writeErr(w, http.StatusServiceUnavailable, firstNonEmpty(why, "AI is not set up (Settings → AI)"))
		return
	}
	l, _ := s.store.ListByID(ctx, c.ListID)
	prompt := fmt.Sprintf("Board: %s\nList: %s\nCard: %s\nDescription:\n%s\n\nSuggest 3 to 8 concrete steps to get this card done. Leave out steps the description already has as a task line. Answer as JSON: {\"items\": [\"step\", …]}",
		b.Title, l.Title, c.Title, excerpt(c.Description, 3000))
	var out struct {
		Items []string `json:"items"`
	}
	if err := cl.JSON(ctx, aiVoice, prompt, &out); err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	items := []string{}
	for _, it := range out.Items {
		it = strings.TrimSpace(strings.TrimLeft(it, "-*•[] "))
		if it != "" && len(items) < 12 {
			items = append(items, excerpt(it, 200))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleAIBoard drafts a board for a goal (nothing is saved; the page
// creates it with POST /boards + template_data once the person likes it).
func (s *Server) handleAIBoard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Goal string `json:"goal"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Goal) == "" {
		writeErr(w, http.StatusBadRequest, "say what the board is for")
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, _, why := s.aiFor(ctx, actorID(ctx), st)
	if cl == nil {
		writeErr(w, http.StatusServiceUnavailable, firstNonEmpty(why, "AI is not set up (Settings → AI)"))
		return
	}
	prompt := "Draft a board for this: " + excerpt(in.Goal, 1000) + `

Answer as JSON: {"title": "short board name", "emoji": "one emoji", "color": "hex colour without #",
"lists": [{"title": "list name", "is_done": false, "cards": ["card title", …]}, …],
"labels": [{"title": "label", "color": "hex without #"}, …]}
3 to 7 lists in the order work moves through them; the last one is where finished cards go ("is_done": true) and has no cards.
Up to 8 starting cards per list, only where they obviously belong. 3 to 6 labels.`
	var t template
	if err := cl.JSON(ctx, aiVoice, prompt, &t); err != nil {
		writeErr(w, http.StatusBadGateway, "AI: "+err.Error())
		return
	}
	t.Key = ""
	t.Title = excerpt(strings.TrimSpace(t.Title), 80)
	t.Color = cleanColor(t.Color)
	if !hexColor.MatchString(t.Color) {
		t.Color = "0c66e4"
	}
	if len(t.Lists) == 0 {
		writeErr(w, http.StatusBadGateway, "the AI gave no lists; try saying a bit more")
		return
	}
	if len(t.Lists) > 12 {
		t.Lists = t.Lists[:12]
	}
	for i := range t.Lists {
		if t.Lists[i].Cards == nil {
			t.Lists[i].Cards = []string{}
		}
		if len(t.Lists[i].Cards) > 15 {
			t.Lists[i].Cards = t.Lists[i].Cards[:15]
		}
	}
	for i := range t.Labels {
		t.Labels[i].Color = cleanColor(t.Labels[i].Color)
		if !hexColor.MatchString(t.Labels[i].Color) {
			t.Labels[i].Color = "8590a2"
		}
	}
	if t.Labels == nil {
		t.Labels = []templateLabel{}
	}
	writeJSON(w, http.StatusOK, t)
}
