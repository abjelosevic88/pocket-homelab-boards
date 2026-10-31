package store

import (
	"encoding/json"
	"strings"
)

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Roles a person can have on a board.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// Board is a board as one person sees it (role, star and order are theirs).
type Board struct {
	ID          int64  `json:"id"`
	OwnerID     int64  `json:"owner_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Emoji       string `json:"emoji"`
	Archived    bool   `json:"archived"`
	Source      string `json:"source"`
	ExternalID  string `json:"external_id"`
	DeletedAt   string `json:"deleted_at"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	Role     string `json:"role"` // owner | editor | viewer
	Starred  bool   `json:"starred"`
	Position int    `json:"position"`
	OpenedAt string `json:"opened_at"`
	Shared   bool   `json:"shared"` // has members besides the owner

	// Filled in lists of boards.
	Cards    int `json:"cards"`
	Open     int `json:"open"`     // cards not done
	Overdue  int `json:"overdue"`  // open cards past due
	DueSoon  int `json:"due_soon"` // open cards due in the next 48 hours
	Assigned int `json:"assigned"` // open cards assigned to me
}

// CanEdit reports whether the role may change content.
func (b *Board) CanEdit() bool { return b.Role == RoleOwner || b.Role == RoleEditor }

// Member is someone who can see a board (the owner included).
type Member struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// List is a column on a board (a "stack" in Nextcloud Deck).
type List struct {
	ID         int64  `json:"id"`
	BoardID    int64  `json:"board_id"`
	Title      string `json:"title"`
	Position   int    `json:"position"`
	WIPLimit   int    `json:"wip_limit"`
	IsDone     bool   `json:"is_done"`
	Color      string `json:"color"`
	ExternalID string `json:"external_id"`
	DeletedAt  string `json:"deleted_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	Cards      []Card `json:"cards,omitempty"`
}

// Label is a coloured tag defined on a board.
type Label struct {
	ID         int64  `json:"id"`
	BoardID    int64  `json:"board_id"`
	Title      string `json:"title"`
	Color      string `json:"color"`
	Position   int    `json:"position"`
	ExternalID string `json:"external_id"`
}

// Card is one card. Labels and Members are ids; the summary counters are
// filled when cards are listed.
type Card struct {
	ID          int64   `json:"id"`
	BoardID     int64   `json:"board_id"`
	ListID      int64   `json:"list_id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Position    int     `json:"position"`
	StartAt     string  `json:"start_at"`
	DueAt       string  `json:"due_at"`
	DoneAt      string  `json:"done_at"`
	Done        bool    `json:"done"`
	Archived    bool    `json:"archived"`
	CoverColor  string  `json:"cover_color"`
	Repeat      string  `json:"repeat"`
	CreatedBy   *int64  `json:"created_by"`
	UpdatedBy   *int64  `json:"updated_by"`
	Source      string  `json:"source"`
	ExternalID  string  `json:"external_id"`
	RemindedAt  string  `json:"-"`
	TaskRef     string  `json:"task_ref"`
	DeletedAt   string  `json:"deleted_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Labels      []int64 `json:"labels"`
	Members     []int64 `json:"members"`

	Comments       int `json:"comments"`
	Attachments    int `json:"attachments"`
	ChecklistDone  int `json:"checklist_done"`
	ChecklistTotal int `json:"checklist_total"`

	// Filled for cards listed across boards (My cards, search).
	BoardTitle string `json:"board_title,omitempty"`
	BoardColor string `json:"board_color,omitempty"`
	ListTitle  string `json:"list_title,omitempty"`
}

// Comment is a note on a card.
type Comment struct {
	ID         int64  `json:"id"`
	CardID     int64  `json:"card_id"`
	UserID     *int64 `json:"user_id"`
	AuthorName string `json:"author_name"`
	ParentID   *int64 `json:"parent_id"`
	Message    string `json:"message"`
	Source     string `json:"source"`
	ExternalID string `json:"-"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Attachment is a file on a card.
type Attachment struct {
	ID        int64  `json:"id"`
	CardID    int64  `json:"card_id"`
	UserID    *int64 `json:"user_id"`
	Filename  string `json:"filename"`
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	Stored    string `json:"-"`
	DeletedAt string `json:"deleted_at"`
	CreatedAt string `json:"created_at"`
}

// Activity is one thing that happened on a board.
type Activity struct {
	ID        int64           `json:"id"`
	BoardID   int64           `json:"board_id"`
	CardID    *int64          `json:"card_id"`
	UserID    *int64          `json:"user_id"`
	UserName  string          `json:"user_name"`
	Kind      string          `json:"kind"`
	Detail    json.RawMessage `json:"detail"`
	Via       string          `json:"via"`
	CreatedAt string          `json:"created_at"`
}

// Checklist counts the Markdown task lines (- [ ] / - [x] / * [ ]) of a description.
func Checklist(desc string) (done, total int) {
	for _, line := range strings.Split(desc, "\n") {
		t := strings.TrimLeft(line, " \t")
		if len(t) < 6 || (t[0] != '-' && t[0] != '*' && t[0] != '+') || t[1] != ' ' || t[2] != '[' || t[4] != ']' {
			continue
		}
		switch t[3] {
		case ' ':
			total++
		case 'x', 'X':
			total++
			done++
		}
	}
	return
}

// Settings are the box-wide values plus one person's preferences and their
// own connections. Connection modes: "core" (an app link through Pocket
// Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday

	// Boards.
	DefaultView    string `json:"default_view"`    // home | mine (where the app opens)
	CardDensity    string `json:"card_density"`    // comfortable | compact
	ShowDoneCards  bool   `json:"show_done_cards"` // done cards stay on the board (dimmed)
	ConfirmDelete  bool   `json:"confirm_delete"`
	DueTimeDefault string `json:"due_time_default"` // HH:MM when only a date is picked

	// Notifications (Pocket Alerts / Apprise / ntfy).
	NotifyDue       bool   `json:"notify_due"`        // a reminder before a card is due
	RemindBeforeMin int    `json:"remind_before_min"` // how long before
	NotifyAssigned  bool   `json:"notify_assigned"`   // someone assigned a card to me
	NotifyComments  bool   `json:"notify_comments"`   // comments on cards I'm on
	NotifyDigest    bool   `json:"notify_digest"`     // a morning list of what is due today
	DigestHour      int    `json:"digest_hour"`
	NotifyURL       string `json:"notify_url"`
	NotifyFormat    string `json:"notify_format"` // apprise | ntfy | json

	// The calendar feed (an .ics address with a secret in it).
	ICSToken string `json:"ics_token"`

	// Pocket Tasks and Pocket Time, through Pocket Core or directly.
	TasksMode      string `json:"tasks_mode"`
	TasksURL       string `json:"tasks_url"`
	TasksToken     string `json:"tasks_token"`
	TasksPublicURL string `json:"tasks_public_url"`

	TimeMode      string `json:"time_mode"`
	TimeURL       string `json:"time_url"`
	TimeToken     string `json:"time_token"`
	TimePublicURL string `json:"time_public_url"`

	// Nextcloud (for importing Deck boards): through Pocket Core or directly.
	NextcloudMode     string `json:"nextcloud_mode"`
	NextcloudURL      string `json:"nextcloud_url"`
	NextcloudUser     string `json:"nextcloud_user"`
	NextcloudPassword string `json:"nextcloud_password"`

	// AI for the reflection and questions: Pocket AI through Core, or an
	// OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }
