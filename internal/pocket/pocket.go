// Package pocket talks to sibling Pocket apps: Pocket Tasks (a card becomes
// a task) and Pocket Time (the shared timer). Same bearer-token JSON API everywhere.
package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

// ErrNotFound is a 404 from the sibling app.
var ErrNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		_ = json.Unmarshal(b, &e)
		return &StatusError{Status: res.StatusCode, Message: e.Error, Body: b}
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app: /healthz plus a token check.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// StatusError is an answer that wasn't 2xx (the body is kept for 409s that explain themselves).
type StatusError struct {
	Status  int
	Message string
	Body    []byte
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// Do calls any JSON endpoint of the app.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, v any) error {
	return c.do(ctx, method, path, q, body, v)
}

// ---------------- Pocket Tasks ----------------

// Task is the part of a Pocket Tasks task Boards reads back.
type Task struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// CreateTask adds a task with a link back to the card.
func (c *Client) CreateTask(ctx context.Context, title, notes, due, link string) (*Task, error) {
	body := map[string]any{"title": title, "notes": notes, "url": link}
	if due != "" {
		body["due"] = due
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/tasks", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------- Pocket Time ----------------

// TimerEntry is Pocket Time's running (or just stopped) timer.
type TimerEntry struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	App       string `json:"app"`
	AppRef    string `json:"app_ref"`
	AppLabel  string `json:"app_label"`
	Source    string `json:"source"`
	StartedAt string `json:"started_at"`
}

// TimerState is GET /timer and the start/stop answers.
type TimerState struct {
	Running *TimerEntry `json:"running"`
	Stopped *TimerEntry `json:"stopped"`
	Error   string      `json:"error"`
}

// Timer reads the running timer.
func (c *Client) Timer(ctx context.Context) (*TimerState, error) {
	var out TimerState
	return &out, c.do(ctx, http.MethodGet, "/api/v1/timer", nil, nil, &out)
}

// StartTimer starts the shared timer for something in Boards. A 409 means
// another timer runs: the answer's Running says which.
func (c *Client) StartTimer(ctx context.Context, body map[string]any) (*TimerState, error) {
	var out TimerState
	err := c.do(ctx, http.MethodPost, "/api/v1/timer/start", nil, body, &out)
	var se *StatusError
	if errors.As(err, &se) && se.Status == http.StatusConflict {
		_ = json.Unmarshal(se.Body, &out)
	}
	return &out, err
}

// StopTimer stops the timer (only the given entry when onlyIf > 0).
func (c *Client) StopTimer(ctx context.Context, onlyIf int64) (*TimerState, error) {
	body := map[string]any{}
	if onlyIf > 0 {
		body["only_if"] = onlyIf
	}
	var out TimerState
	return &out, c.do(ctx, http.MethodPost, "/api/v1/timer/stop", nil, body, &out)
}
