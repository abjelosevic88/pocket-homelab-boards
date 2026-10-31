package store

import (
	"context"
	"database/sql"
)

// Worklog is time someone spent on a card.
type Worklog struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	CardID     int64  `json:"card_id"`
	Title      string `json:"title"`
	Note       string `json:"note"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at"`
	Date       string `json:"date"`
	DurationS  int    `json:"duration_s"`
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	CreatedAt  string `json:"created_at"`
}

const worklogCols = `id, user_id, card_id, title, note, started_at, ended_at, date, duration_s, source, external_id, created_at`

func scanWorklog(row interface{ Scan(...any) error }) (*Worklog, error) {
	var w Worklog
	if err := row.Scan(&w.ID, &w.UserID, &w.CardID, &w.Title, &w.Note, &w.StartedAt, &w.EndedAt, &w.Date, &w.DurationS, &w.Source, &w.ExternalID, &w.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	return &w, nil
}

func (s *Store) queryWorklog(ctx context.Context, q string, args ...any) ([]Worklog, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+worklogCols+` FROM worklog `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Worklog{}
	for rows.Next() {
		w, err := scanWorklog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// WorklogRange is one person's logged time between two dates (inclusive).
func (s *Store) WorklogRange(ctx context.Context, uid int64, from, to string) ([]Worklog, error) {
	return s.queryWorklog(ctx, `WHERE user_id = ? AND date >= ? AND date <= ? ORDER BY date, started_at, id`, uid, from, to)
}

// CardWorklog is everyone's time on one card.
func (s *Store) CardWorklog(ctx context.Context, cardID int64) ([]Worklog, error) {
	return s.queryWorklog(ctx, `WHERE card_id = ? ORDER BY date DESC, id DESC`, cardID)
}

// WorklogByID returns one of a person's entries.
func (s *Store) WorklogByID(ctx context.Context, uid, id int64) (*Worklog, error) {
	return scanWorklog(s.DB.QueryRowContext(ctx, `SELECT `+worklogCols+` FROM worklog WHERE user_id = ? AND id = ?`, uid, id))
}

// SaveWorklog adds an entry, or updates the one with the same external id.
func (s *Store) SaveWorklog(ctx context.Context, w *Worklog) error {
	if w.Source == "" {
		w.Source = "app"
	}
	if w.ExternalID != "" {
		var id int64
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM worklog WHERE user_id = ? AND external_id = ?`, w.UserID, w.ExternalID).Scan(&id)
		if err == nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE worklog SET card_id=?, title=?, note=?, started_at=?, ended_at=?, date=?, duration_s=?, source=? WHERE id=?`,
				w.CardID, w.Title, w.Note, w.StartedAt, w.EndedAt, w.Date, w.DurationS, w.Source, id)
			w.ID = id
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO worklog (user_id, card_id, title, note, started_at, ended_at, date, duration_s, source, external_id) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		w.UserID, w.CardID, w.Title, w.Note, w.StartedAt, w.EndedAt, w.Date, w.DurationS, w.Source, w.ExternalID)
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	return nil
}

// DeleteWorklog removes one of a person's entries.
func (s *Store) DeleteWorklog(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM worklog WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CardTime is the total seconds logged on a card (everyone's).
func (s *Store) CardTime(ctx context.Context, cardID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(duration_s), 0) FROM worklog WHERE card_id = ?`, cardID).Scan(&n)
	return n
}
