package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ---- live updates ----
//
// Everyone looking at a board keeps one Server-Sent Events stream open; any
// change to the board sends a small "changed" note and the page refetches.
// Nothing about the change itself travels here, so the stream never shows
// more than the board the person can already see.

type liveEvent struct {
	BoardID int64  `json:"board_id"`
	By      int64  `json:"by"`
	Kind    string `json:"kind"`
	CardID  int64  `json:"card_id,omitempty"`
}

type hub struct {
	mu   sync.Mutex
	subs map[int64]map[chan liveEvent]struct{}
}

func newHub() *hub { return &hub{subs: map[int64]map[chan liveEvent]struct{}{}} }

func (h *hub) subscribe(boardID int64) chan liveEvent {
	ch := make(chan liveEvent, 16)
	h.mu.Lock()
	if h.subs[boardID] == nil {
		h.subs[boardID] = map[chan liveEvent]struct{}{}
	}
	h.subs[boardID][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(boardID int64, ch chan liveEvent) {
	h.mu.Lock()
	delete(h.subs[boardID], ch)
	if len(h.subs[boardID]) == 0 {
		delete(h.subs, boardID)
	}
	h.mu.Unlock()
}

// send tells everyone watching the board; a slow listener just misses a
// note (the next one refetches the same board anyway).
func (h *hub) send(e liveEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[e.BoardID] {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *Server) handleBoardEvents(w http.ResponseWriter, r *http.Request) {
	b := s.boardFor(w, r, idParam(r, "id"), false)
	if b == nil {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	// The API's 120 s timeout must not cut a stream short.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n: hello\n\n")
	fl.Flush()
	ch := s.hub.subscribe(b.ID)
	defer s.hub.unsubscribe(b.ID, ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: changed\ndata: %s\n\n", data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
