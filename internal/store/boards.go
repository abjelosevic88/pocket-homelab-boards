package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// ---- boards ----
//
// A person sees a board when they own it or it is shared with them. Every
// read below that starts from a board id goes through BoardFor first (in the
// server), so "not yours" and "does not exist" both answer ErrNotFound.

const boardCols = `b.id, b.user_id, b.title, b.description, b.color, b.emoji, b.archived, b.source, b.external_id, b.deleted_at, b.created_at, b.updated_at,
	CASE WHEN b.user_id = ? THEN 'owner' ELSE COALESCE(m.role, '') END,
	COALESCE(p.starred, 0), COALESCE(p.position, 0), COALESCE(p.opened_at, ''),
	EXISTS (SELECT 1 FROM board_members x WHERE x.board_id = b.id)`

const boardFrom = ` FROM boards b
	LEFT JOIN board_members m ON m.board_id = b.id AND m.user_id = ?
	LEFT JOIN board_prefs p ON p.board_id = b.id AND p.user_id = ?
	WHERE (b.user_id = ? OR m.user_id IS NOT NULL)`

func scanBoard(row interface{ Scan(...any) error }) (*Board, error) {
	var b Board
	var archived, starred, shared int
	err := row.Scan(&b.ID, &b.OwnerID, &b.Title, &b.Description, &b.Color, &b.Emoji, &archived, &b.Source, &b.ExternalID, &b.DeletedAt, &b.CreatedAt, &b.UpdatedAt,
		&b.Role, &starred, &b.Position, &b.OpenedAt, &shared)
	if err != nil {
		return nil, notFound(err)
	}
	b.Archived, b.Starred, b.Shared = archived == 1, starred == 1, shared == 1
	return &b, nil
}

// BoardFor returns a board the person can see (not in the bin).
func (s *Store) BoardFor(ctx context.Context, uid, id int64) (*Board, error) {
	return scanBoard(s.DB.QueryRowContext(ctx, `SELECT `+boardCols+boardFrom+` AND b.id = ? AND b.deleted_at = ''`, uid, uid, uid, uid, id))
}

// BoardForAny is BoardFor including boards in the bin.
func (s *Store) BoardForAny(ctx context.Context, uid, id int64) (*Board, error) {
	return scanBoard(s.DB.QueryRowContext(ctx, `SELECT `+boardCols+boardFrom+` AND b.id = ?`, uid, uid, uid, uid, id))
}

// BoardFilter narrows ListBoards.
type BoardFilter struct {
	Archived *bool // nil = both
	Deleted  bool  // the bin instead of live boards
	Counts   bool  // fill the card counters
}

// ListBoards lists the boards a person can see, in their own order.
func (s *Store) ListBoards(ctx context.Context, uid int64, f BoardFilter) ([]Board, error) {
	q := `SELECT ` + boardCols + boardFrom
	args := []any{uid, uid, uid, uid}
	if f.Deleted {
		q += ` AND b.deleted_at <> ''`
	} else {
		q += ` AND b.deleted_at = ''`
	}
	if f.Archived != nil {
		q += ` AND b.archived = ?`
		args = append(args, boolInt(*f.Archived))
	}
	q += ` ORDER BY COALESCE(p.position, 0), b.id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Board{}
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f.Counts && len(out) > 0 {
		now := time.Now().UTC()
		soon := now.Add(48 * time.Hour).Format(time.RFC3339)
		nows := now.Format(time.RFC3339)
		idx := map[int64]int{}
		ids := make([]any, 0, len(out))
		for i, b := range out {
			idx[b.ID] = i
			ids = append(ids, b.ID)
		}
		cq := `SELECT c.board_id, COUNT(1),
			SUM(c.done_at = ''),
			SUM(c.done_at = '' AND c.due_at <> '' AND c.due_at < ?),
			SUM(c.done_at = '' AND c.due_at <> '' AND c.due_at >= ? AND c.due_at < ?),
			SUM(c.done_at = '' AND EXISTS (SELECT 1 FROM card_members cm WHERE cm.card_id = c.id AND cm.user_id = ?))
			FROM cards c JOIN lists l ON l.id = c.list_id
			WHERE c.deleted_at = '' AND c.archived = 0 AND l.deleted_at = '' AND c.board_id IN (` + placeholders(len(ids)) + `) GROUP BY c.board_id`
		rows, err := s.DB.QueryContext(ctx, cq, append([]any{nows, nows, soon, uid}, ids...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var n, open, over, dueSoon, mine int
			if err := rows.Scan(&id, &n, &open, &over, &dueSoon, &mine); err != nil {
				return nil, err
			}
			b := &out[idx[id]]
			b.Cards, b.Open, b.Overdue, b.DueSoon, b.Assigned = n, open, over, dueSoon, mine
		}
		return out, rows.Err()
	}
	return out, nil
}

// AccessibleBoardIDs are the live boards a person can see.
func (s *Store) AccessibleBoardIDs(ctx context.Context, uid int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id FROM boards b LEFT JOIN board_members m ON m.board_id = b.id AND m.user_id = ?
		WHERE (b.user_id = ? OR m.user_id IS NOT NULL) AND b.deleted_at = ''`, uid, uid)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

// CreateBoard adds a board owned by uid, last in their order.
func (s *Store) CreateBoard(ctx context.Context, uid int64, b *Board) error {
	if b.Source == "" {
		b.Source = "app"
	}
	if b.Color == "" {
		b.Color = "0c66e4"
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO boards (user_id, title, description, color, emoji, archived, source, external_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		uid, b.Title, b.Description, b.Color, b.Emoji, boolInt(b.Archived), b.Source, b.ExternalID, now, now)
	if err != nil {
		return err
	}
	b.ID, _ = res.LastInsertId()
	var pos int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM board_prefs WHERE user_id = ?`, uid).Scan(&pos)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO board_prefs (user_id, board_id, position) VALUES (?,?,?)`, uid, b.ID, pos); err != nil {
		return err
	}
	nb, err := s.BoardFor(ctx, uid, b.ID)
	if err != nil {
		return err
	}
	*b = *nb
	return nil
}

// UpdateBoard saves the shared fields of a board.
func (s *Store) UpdateBoard(ctx context.Context, b *Board) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE boards SET title=?, description=?, color=?, emoji=?, archived=?, updated_at=? WHERE id=?`,
		b.Title, b.Description, b.Color, b.Emoji, boolInt(b.Archived), Now(), b.ID)
	return err
}

// TouchBoard marks a board changed (Deck clients sync on lastModified).
func (s *Store) TouchBoard(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE boards SET updated_at=? WHERE id=?`, Now(), id)
}

// SetBoardPref changes one person's star / order / last-opened for a board.
func (s *Store) SetBoardPref(ctx context.Context, uid, boardID int64, starred *bool, position *int, opened bool) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO board_prefs (user_id, board_id, position) VALUES (?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM board_prefs WHERE user_id = ?)) ON CONFLICT DO NOTHING`, uid, boardID, uid); err != nil {
		return err
	}
	if starred != nil {
		if _, err := s.DB.ExecContext(ctx, `UPDATE board_prefs SET starred=? WHERE user_id=? AND board_id=?`, boolInt(*starred), uid, boardID); err != nil {
			return err
		}
	}
	if opened {
		if _, err := s.DB.ExecContext(ctx, `UPDATE board_prefs SET opened_at=? WHERE user_id=? AND board_id=?`, Now(), uid, boardID); err != nil {
			return err
		}
	}
	if position != nil {
		ids, err := s.AccessibleBoardIDs(ctx, uid)
		if err != nil {
			return err
		}
		// current order, then move boardID to *position
		rows, err := s.DB.QueryContext(ctx, `SELECT board_id FROM board_prefs WHERE user_id = ? ORDER BY position, board_id`, uid)
		if err != nil {
			return err
		}
		order, err := scanIDs(rows)
		if err != nil {
			return err
		}
		live := map[int64]bool{}
		for _, id := range ids {
			live[id] = true
		}
		var rest []int64
		for _, id := range order {
			if id != boardID && live[id] {
				rest = append(rest, id)
			}
		}
		order = insertAt(rest, boardID, *position)
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for i, id := range order {
			if _, err := tx.ExecContext(ctx, `UPDATE board_prefs SET position=? WHERE user_id=? AND board_id=?`, i, uid, id); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	}
	return nil
}

// TrashBoard puts a board in the bin (restorable); PurgeBoard removes it
// for good and returns the stored names of its files.
func (s *Store) TrashBoard(ctx context.Context, id int64, trashed bool) error {
	v := ""
	if trashed {
		v = Now()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE boards SET deleted_at=?, updated_at=? WHERE id=?`, v, Now(), id)
	return err
}

func (s *Store) PurgeBoard(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.stored FROM attachments a JOIN cards c ON c.id = a.card_id WHERE c.board_id = ?`, id)
	if err != nil {
		return nil, err
	}
	files, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM boards WHERE id = ?`, id)
	return files, err
}

// BoardByExternal finds a board a person owns by where it came from.
func (s *Store) BoardByExternal(ctx context.Context, uid int64, source, ext string) (*Board, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM boards WHERE user_id=? AND source=? AND external_id=? ORDER BY id LIMIT 1`, uid, source, ext).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.BoardForAny(ctx, uid, id)
}

// ---- members ----

// Members lists who can see a board, the owner first.
func (s *Store) Members(ctx context.Context, boardID int64) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.name, u.email, 'owner' FROM boards b JOIN users u ON u.id = b.user_id WHERE b.id = ?
		UNION ALL
		SELECT u.id, u.name, u.email, m.role FROM board_members m JOIN users u ON u.id = m.user_id WHERE m.board_id = ?`, boardID, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemberIDs are the user ids that can see a board.
func (s *Store) MemberIDs(ctx context.Context, boardID int64) ([]int64, error) {
	ms, err := s.Members(ctx, boardID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.UserID)
	}
	return out, nil
}

// SetMember shares a board with someone (or changes their role).
func (s *Store) SetMember(ctx context.Context, boardID, uid int64, role string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO board_members (board_id, user_id, role) VALUES (?,?,?) ON CONFLICT(board_id, user_id) DO UPDATE SET role = excluded.role`, boardID, uid, role)
	if err == nil {
		s.TouchBoard(ctx, boardID)
	}
	return err
}

// RemoveMember stops sharing a board with someone; their card assignments
// and their own view of the board go with it.
func (s *Store) RemoveMember(ctx context.Context, boardID, uid int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM board_members WHERE board_id=? AND user_id=?`,
		`DELETE FROM card_members WHERE user_id=? AND card_id IN (SELECT id FROM cards WHERE board_id=?)`,
		`DELETE FROM board_prefs WHERE board_id=? AND user_id=?`,
	} {
		a, b := any(boardID), any(uid)
		if strings.Contains(q, "card_members") {
			a, b = uid, boardID
		}
		if _, err := tx.ExecContext(ctx, q, a, b); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.TouchBoard(ctx, boardID)
	return nil
}

// ---- lists ----

const listCols = `id, board_id, title, position, wip_limit, is_done, color, external_id, deleted_at, created_at, updated_at`

func scanList(row interface{ Scan(...any) error }) (*List, error) {
	var l List
	var done int
	if err := row.Scan(&l.ID, &l.BoardID, &l.Title, &l.Position, &l.WIPLimit, &done, &l.Color, &l.ExternalID, &l.DeletedAt, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	l.IsDone = done == 1
	return &l, nil
}

// Lists returns a board's lists in order (deleted ones only when asked).
func (s *Store) Lists(ctx context.Context, boardID int64, deleted bool) ([]List, error) {
	q := `SELECT ` + listCols + ` FROM lists WHERE board_id = ? AND deleted_at = '' ORDER BY position, id`
	if deleted {
		q = `SELECT ` + listCols + ` FROM lists WHERE board_id = ? AND deleted_at <> '' ORDER BY position, id`
	}
	rows, err := s.DB.QueryContext(ctx, q, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// ListByID returns a list (including deleted ones; callers check the board).
func (s *Store) ListByID(ctx context.Context, id int64) (*List, error) {
	return scanList(s.DB.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists WHERE id = ?`, id))
}

// CreateList adds a list at position (-1 = at the end).
func (s *Store) CreateList(ctx context.Context, l *List, position int) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO lists (board_id, title, position, wip_limit, is_done, color, external_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		l.BoardID, l.Title, 1<<30, l.WIPLimit, boolInt(l.IsDone), l.Color, l.ExternalID, now, now)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	if err := s.MoveList(ctx, l.ID, position); err != nil {
		return err
	}
	nl, err := s.ListByID(ctx, l.ID)
	if err == nil {
		*l = *nl
	}
	return err
}

// UpdateList saves a list's fields (not its position).
func (s *Store) UpdateList(ctx context.Context, l *List) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE lists SET title=?, wip_limit=?, is_done=?, color=?, updated_at=? WHERE id=?`, l.Title, l.WIPLimit, boolInt(l.IsDone), l.Color, Now(), l.ID)
	if err == nil {
		s.TouchBoard(ctx, l.BoardID)
	}
	return err
}

// MoveList puts a list at position (-1 = at the end) and renumbers the board's lists.
func (s *Store) MoveList(ctx context.Context, id int64, position int) error {
	l, err := s.ListByID(ctx, id)
	if err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM lists WHERE board_id = ? AND deleted_at = '' AND id <> ? ORDER BY position, id`, l.BoardID, id)
	if err != nil {
		return err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	if err := s.renumber(ctx, "lists", insertAt(ids, id, position)); err != nil {
		return err
	}
	s.TouchBoard(ctx, l.BoardID)
	return nil
}

// TrashList deletes a list (restorable, with its cards inside).
func (s *Store) TrashList(ctx context.Context, id int64, trashed bool) error {
	v := ""
	if trashed {
		v = Now()
	}
	l, err := s.ListByID(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE lists SET deleted_at=?, updated_at=? WHERE id=?`, v, Now(), id); err != nil {
		return err
	}
	s.TouchBoard(ctx, l.BoardID)
	return nil
}

// ListByExternal finds an imported list on a board.
func (s *Store) ListByExternal(ctx context.Context, boardID int64, ext string) (*List, error) {
	return scanList(s.DB.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists WHERE board_id = ? AND external_id = ? ORDER BY id LIMIT 1`, boardID, ext))
}

// ---- labels ----

// Labels returns a board's labels.
func (s *Store) Labels(ctx context.Context, boardID int64) ([]Label, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, board_id, title, color, position, external_id FROM labels WHERE board_id = ? ORDER BY position, id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.BoardID, &l.Title, &l.Color, &l.Position, &l.ExternalID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LabelByID returns a label.
func (s *Store) LabelByID(ctx context.Context, id int64) (*Label, error) {
	var l Label
	err := s.DB.QueryRowContext(ctx, `SELECT id, board_id, title, color, position, external_id FROM labels WHERE id = ?`, id).Scan(&l.ID, &l.BoardID, &l.Title, &l.Color, &l.Position, &l.ExternalID)
	if err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// CreateLabel adds a label to a board.
func (s *Store) CreateLabel(ctx context.Context, l *Label) error {
	var pos int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM labels WHERE board_id = ?`, l.BoardID).Scan(&pos)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO labels (board_id, title, color, position, external_id) VALUES (?,?,?,?,?)`, l.BoardID, l.Title, l.Color, pos, l.ExternalID)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	l.Position = pos
	s.TouchBoard(ctx, l.BoardID)
	return nil
}

// UpdateLabel saves a label.
func (s *Store) UpdateLabel(ctx context.Context, l *Label) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE labels SET title=?, color=?, position=? WHERE id=?`, l.Title, l.Color, l.Position, l.ID)
	if err == nil {
		s.TouchBoard(ctx, l.BoardID)
	}
	return err
}

// DeleteLabel removes a label (and takes it off every card).
func (s *Store) DeleteLabel(ctx context.Context, l *Label) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM labels WHERE id=?`, l.ID)
	if err == nil {
		s.TouchBoard(ctx, l.BoardID)
	}
	return err
}

// ---- cards ----

const cardCols = `c.id, c.board_id, c.list_id, c.title, c.description, c.position, c.start_at, c.due_at, c.done_at, c.archived, c.cover_color, c.repeat,
	c.created_by, c.updated_by, c.source, c.external_id, c.reminded_at, c.task_ref, c.deleted_at, c.created_at, c.updated_at,
	(SELECT COUNT(1) FROM comments x WHERE x.card_id = c.id),
	(SELECT COUNT(1) FROM attachments x WHERE x.card_id = c.id AND x.deleted_at = '')`

func scanCard(row interface{ Scan(...any) error }, extra ...any) (*Card, error) {
	var c Card
	var archived int
	var cb, ub sql.NullInt64
	dst := []any{&c.ID, &c.BoardID, &c.ListID, &c.Title, &c.Description, &c.Position, &c.StartAt, &c.DueAt, &c.DoneAt, &archived, &c.CoverColor, &c.Repeat,
		&cb, &ub, &c.Source, &c.ExternalID, &c.RemindedAt, &c.TaskRef, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt, &c.Comments, &c.Attachments}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, notFound(err)
	}
	c.Archived = archived == 1
	c.Done = c.DoneAt != ""
	if cb.Valid {
		c.CreatedBy = &cb.Int64
	}
	if ub.Valid {
		c.UpdatedBy = &ub.Int64
	}
	c.ChecklistDone, c.ChecklistTotal = Checklist(c.Description)
	c.Labels, c.Members = []int64{}, []int64{}
	return &c, nil
}

// fillCardLinks loads label and member ids for the cards.
func (s *Store) fillCardLinks(ctx context.Context, cards []Card) error {
	if len(cards) == 0 {
		return nil
	}
	idx := map[int64]int{}
	ids := make([]any, 0, len(cards))
	for i, c := range cards {
		idx[c.ID] = i
		ids = append(ids, c.ID)
	}
	for _, q := range []struct {
		sql    string
		labels bool
	}{
		{`SELECT cl.card_id, cl.label_id FROM card_labels cl JOIN labels l ON l.id = cl.label_id WHERE cl.card_id IN (` + placeholders(len(ids)) + `) ORDER BY l.position, l.id`, true},
		{`SELECT card_id, user_id FROM card_members WHERE card_id IN (` + placeholders(len(ids)) + `) ORDER BY user_id`, false},
	} {
		rows, err := s.DB.QueryContext(ctx, q.sql, ids...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var cid, x int64
			if err := rows.Scan(&cid, &x); err != nil {
				rows.Close()
				return err
			}
			c := &cards[idx[cid]]
			if q.labels {
				c.Labels = append(c.Labels, x)
			} else {
				c.Members = append(c.Members, x)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) queryCards(ctx context.Context, q string, args ...any) ([]Card, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Card{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.fillCardLinks(ctx, out)
}

// BoardCards returns the cards on a board's live lists, in list order.
func (s *Store) BoardCards(ctx context.Context, boardID int64, archived bool) ([]Card, error) {
	return s.queryCards(ctx, `SELECT `+cardCols+` FROM cards c JOIN lists l ON l.id = c.list_id
		WHERE c.board_id = ? AND c.deleted_at = '' AND l.deleted_at = '' AND c.archived = ? ORDER BY l.position, c.position, c.id`, boardID, boolInt(archived))
}

// ListCards returns the live cards of one list.
func (s *Store) ListCards(ctx context.Context, listID int64, archived bool) ([]Card, error) {
	return s.queryCards(ctx, `SELECT `+cardCols+` FROM cards c WHERE c.list_id = ? AND c.deleted_at = '' AND c.archived = ? ORDER BY c.position, c.id`, listID, boolInt(archived))
}

// CardByID returns a card (including deleted ones; callers check the board).
func (s *Store) CardByID(ctx context.Context, id int64) (*Card, error) {
	cs, err := s.queryCards(ctx, `SELECT `+cardCols+` FROM cards c WHERE c.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, ErrNotFound
	}
	return &cs[0], nil
}

// CardByExternal finds an imported card on a board.
func (s *Store) CardByExternal(ctx context.Context, boardID int64, source, ext string) (*Card, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM cards WHERE board_id=? AND source=? AND external_id=? ORDER BY id LIMIT 1`, boardID, source, ext).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.CardByID(ctx, id)
}

// CreateCard adds a card to a list at position (-1 = at the bottom, 0 = top).
func (s *Store) CreateCard(ctx context.Context, uid int64, c *Card, position int) error {
	if c.Source == "" {
		c.Source = "app"
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO cards (board_id, list_id, title, description, position, start_at, due_at, done_at, archived, cover_color, repeat, created_by, updated_by, source, external_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.BoardID, c.ListID, c.Title, c.Description, 1<<30, c.StartAt, c.DueAt, c.DoneAt, boolInt(c.Archived), c.CoverColor, c.Repeat, nullI(&uid), nullI(&uid), c.Source, c.ExternalID,
		firstNonEmpty(c.CreatedAt, now), now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	for _, l := range c.Labels {
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO card_labels (card_id, label_id) SELECT ?, id FROM labels WHERE id = ? AND board_id = ?`, c.ID, l, c.BoardID); err != nil {
			return err
		}
	}
	for _, m := range c.Members {
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO card_members (card_id, user_id) VALUES (?,?)`, c.ID, m); err != nil {
			return err
		}
	}
	if err := s.MoveCard(ctx, c.ID, c.ListID, position); err != nil {
		return err
	}
	nc, err := s.CardByID(ctx, c.ID)
	if err == nil {
		*c = *nc
	}
	return err
}

// UpdateCard saves a card's own fields (not its list or position).
func (s *Store) UpdateCard(ctx context.Context, uid int64, c *Card) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cards SET title=?, description=?, start_at=?, due_at=?, done_at=?, archived=?, cover_color=?, repeat=?, task_ref=?,
		reminded_at = CASE WHEN due_at <> ? THEN '' ELSE reminded_at END, updated_by=?, updated_at=? WHERE id=?`,
		c.Title, c.Description, c.StartAt, c.DueAt, c.DoneAt, boolInt(c.Archived), c.CoverColor, c.Repeat, c.TaskRef, c.DueAt, nullI(&uid), Now(), c.ID)
	if err == nil {
		s.TouchBoard(ctx, c.BoardID)
	}
	return err
}

// MoveCard puts a card in a list (of the same board) at position (-1 = bottom)
// and renumbers the lists it left and joined.
func (s *Store) MoveCard(ctx context.Context, id, listID int64, position int) error {
	c, err := s.CardByID(ctx, id)
	if err != nil {
		return err
	}
	l, err := s.ListByID(ctx, listID)
	if err != nil {
		return err
	}
	if l.BoardID != c.BoardID {
		return ErrNotFound
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE cards SET list_id=?, updated_at=? WHERE id=?`, listID, Now(), id); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM cards WHERE list_id = ? AND deleted_at = '' AND archived = 0 AND id <> ? ORDER BY position, id`, listID, id)
	if err != nil {
		return err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	if err := s.renumber(ctx, "cards", insertAt(ids, id, position)); err != nil {
		return err
	}
	if c.ListID != listID {
		rows, err := s.DB.QueryContext(ctx, `SELECT id FROM cards WHERE list_id = ? AND deleted_at = '' AND archived = 0 ORDER BY position, id`, c.ListID)
		if err != nil {
			return err
		}
		old, err := scanIDs(rows)
		if err != nil {
			return err
		}
		if err := s.renumber(ctx, "cards", old); err != nil {
			return err
		}
	}
	s.TouchBoard(ctx, c.BoardID)
	return nil
}

// MoveCardToBoard moves a card to a list on another board. Labels are
// matched by title (missing ones are created there) and assignees who
// cannot see the new board are dropped.
func (s *Store) MoveCardToBoard(ctx context.Context, id, listID int64, position int) error {
	c, err := s.CardByID(ctx, id)
	if err != nil {
		return err
	}
	l, err := s.ListByID(ctx, listID)
	if err != nil {
		return err
	}
	if l.BoardID == c.BoardID {
		return s.MoveCard(ctx, id, listID, position)
	}
	oldLabels, _ := s.Labels(ctx, c.BoardID)
	newLabels, _ := s.Labels(ctx, l.BoardID)
	byTitle := map[string]int64{}
	for _, nl := range newLabels {
		byTitle[strings.ToLower(nl.Title)+"|"+nl.Color] = nl.ID
	}
	var keep []int64
	for _, ol := range oldLabels {
		on := false
		for _, x := range c.Labels {
			on = on || x == ol.ID
		}
		if !on {
			continue
		}
		k := strings.ToLower(ol.Title) + "|" + ol.Color
		nid, ok := byTitle[k]
		if !ok {
			nl := Label{BoardID: l.BoardID, Title: ol.Title, Color: ol.Color}
			if err := s.CreateLabel(ctx, &nl); err != nil {
				return err
			}
			nid = nl.ID
			byTitle[k] = nid
		}
		keep = append(keep, nid)
	}
	members, _ := s.MemberIDs(ctx, l.BoardID)
	can := map[int64]bool{}
	for _, m := range members {
		can[m] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM card_labels WHERE card_id = ?`, []any{id}},
		{`UPDATE cards SET board_id = ?, updated_at = ? WHERE id = ?`, []any{l.BoardID, Now(), id}},
	}
	for _, m := range c.Members {
		if !can[m] {
			stmts = append(stmts, struct {
				q    string
				args []any
			}{`DELETE FROM card_members WHERE card_id = ? AND user_id = ?`, []any{id, m}})
		}
	}
	for _, lid := range keep {
		stmts = append(stmts, struct {
			q    string
			args []any
		}{`INSERT OR IGNORE INTO card_labels (card_id, label_id) VALUES (?, ?)`, []any{id, lid}})
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE activity SET board_id = ? WHERE card_id = ?`, l.BoardID, id); err != nil {
		return err
	}
	s.TouchBoard(ctx, c.BoardID)
	return s.MoveCard(ctx, id, listID, position)
}

// TrashCard deletes a card (restorable from the bin) or brings it back.
func (s *Store) TrashCard(ctx context.Context, c *Card, trashed bool) error {
	v := ""
	if trashed {
		v = Now()
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE cards SET deleted_at=?, updated_at=? WHERE id=?`, v, Now(), c.ID); err != nil {
		return err
	}
	if !trashed {
		return s.MoveCard(ctx, c.ID, c.ListID, -1)
	}
	s.TouchBoard(ctx, c.BoardID)
	return nil
}

// PurgeCard removes a card for good and returns its stored file names.
func (s *Store) PurgeCard(ctx context.Context, c *Card) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT stored FROM attachments WHERE card_id = ?`, c.ID)
	if err != nil {
		return nil, err
	}
	files, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM cards WHERE id = ?`, c.ID); err != nil {
		return nil, err
	}
	s.TouchBoard(ctx, c.BoardID)
	return files, nil
}

// SetCardLabel puts a label on a card or takes it off (the label must be the board's).
func (s *Store) SetCardLabel(ctx context.Context, c *Card, labelID int64, on bool) error {
	var err error
	if on {
		var res sql.Result
		res, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO card_labels (card_id, label_id) SELECT ?, id FROM labels WHERE id = ? AND board_id = ?`, c.ID, labelID, c.BoardID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				if _, e := s.LabelByID(ctx, labelID); e != nil {
					return ErrNotFound
				}
			}
		}
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM card_labels WHERE card_id = ? AND label_id = ?`, c.ID, labelID)
	}
	if err == nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE cards SET updated_at=? WHERE id=?`, Now(), c.ID)
		s.TouchBoard(ctx, c.BoardID)
	}
	return err
}

// SetCardMember assigns a card to someone or unassigns them (they must see the board — checked by the caller).
func (s *Store) SetCardMember(ctx context.Context, c *Card, uid int64, on bool) (bool, error) {
	var res sql.Result
	var err error
	if on {
		res, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO card_members (card_id, user_id) VALUES (?, ?)`, c.ID, uid)
	} else {
		res, err = s.DB.ExecContext(ctx, `DELETE FROM card_members WHERE card_id = ? AND user_id = ?`, c.ID, uid)
	}
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	_, _ = s.DB.ExecContext(ctx, `UPDATE cards SET updated_at=? WHERE id=?`, Now(), c.ID)
	s.TouchBoard(ctx, c.BoardID)
	return n > 0, nil
}

// SetReminded records that the due reminder for this due date went out.
func (s *Store) SetReminded(ctx context.Context, id int64, dueAt string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cards SET reminded_at = ? WHERE id = ? AND due_at = ?`, dueAt, id, dueAt)
	return err
}

// CardFilter narrows cards across every board a person can see.
type CardFilter struct {
	Assigned    bool   // only cards assigned to me
	DueFrom     string // RFC3339, inclusive
	DueTo       string // RFC3339, exclusive
	HasDue      bool
	Q           string // words in the title or description
	BoardID     int64
	IncludeDone bool
	Archived    bool
	Limit       int
}

// FindCards lists cards across the boards a person can see.
func (s *Store) FindCards(ctx context.Context, uid int64, f CardFilter) ([]Card, error) {
	q := `SELECT ` + cardCols + `, b.title, b.color, l.title FROM cards c
		JOIN boards b ON b.id = c.board_id JOIN lists l ON l.id = c.list_id
		LEFT JOIN board_members m ON m.board_id = b.id AND m.user_id = ?
		WHERE (b.user_id = ? OR m.user_id IS NOT NULL) AND b.deleted_at = '' AND b.archived = 0 AND c.deleted_at = '' AND l.deleted_at = '' AND c.archived = ?`
	args := []any{uid, uid, boolInt(f.Archived)}
	if f.Assigned {
		q += ` AND EXISTS (SELECT 1 FROM card_members cm WHERE cm.card_id = c.id AND cm.user_id = ?)`
		args = append(args, uid)
	}
	if !f.IncludeDone {
		q += ` AND c.done_at = ''`
	}
	if f.HasDue {
		q += ` AND c.due_at <> ''`
	}
	if f.DueFrom != "" {
		q += ` AND c.due_at <> '' AND c.due_at >= ?`
		args = append(args, f.DueFrom)
	}
	if f.DueTo != "" {
		q += ` AND c.due_at <> '' AND c.due_at < ?`
		args = append(args, f.DueTo)
	}
	if f.BoardID > 0 {
		q += ` AND c.board_id = ?`
		args = append(args, f.BoardID)
	}
	for _, w := range strings.Fields(f.Q) {
		q += ` AND (c.title LIKE ? ESCAPE '\' OR c.description LIKE ? ESCAPE '\')`
		like := "%" + escapeLike(w) + "%"
		args = append(args, like, like)
	}
	q += ` ORDER BY CASE WHEN c.due_at = '' THEN 1 ELSE 0 END, c.due_at, c.updated_at DESC`
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	q += ` LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []Card{}
	for rows.Next() {
		var bt, bc, lt string
		c, err := scanCard(rows, &bt, &bc, &lt)
		if err != nil {
			rows.Close()
			return nil, err
		}
		c.BoardTitle, c.BoardColor, c.ListTitle = bt, bc, lt
		out = append(out, *c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.fillCardLinks(ctx, out)
}

// DueToRemind lists open cards on live boards due before `before` whose
// reminder for that due date has not gone out yet (every person's).
func (s *Store) DueToRemind(ctx context.Context, before string) ([]Card, error) {
	return s.queryCards(ctx, `SELECT `+cardCols+` FROM cards c JOIN boards b ON b.id = c.board_id JOIN lists l ON l.id = c.list_id
		WHERE c.due_at <> '' AND c.due_at <= ? AND c.reminded_at <> c.due_at AND c.done_at = '' AND c.deleted_at = '' AND c.archived = 0
		AND b.deleted_at = '' AND b.archived = 0 AND l.deleted_at = '' ORDER BY c.due_at LIMIT 500`, before)
}

// TrashedCards lists the deleted cards on the given boards.
func (s *Store) TrashedCards(ctx context.Context, boardIDs []int64) ([]Card, error) {
	if len(boardIDs) == 0 {
		return []Card{}, nil
	}
	args := make([]any, len(boardIDs))
	for i, id := range boardIDs {
		args[i] = id
	}
	return s.queryCards(ctx, `SELECT `+cardCols+` FROM cards c WHERE c.deleted_at <> '' AND c.board_id IN (`+placeholders(len(args))+`) ORDER BY c.deleted_at DESC LIMIT 500`, args...)
}

// PurgeTrash removes what has been in the bin longer than the cutoff and
// returns the stored files to delete.
func (s *Store) PurgeTrash(ctx context.Context, before string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.stored FROM attachments a JOIN cards c ON c.id = a.card_id JOIN boards b ON b.id = c.board_id
		WHERE (c.deleted_at <> '' AND c.deleted_at < ?) OR (b.deleted_at <> '' AND b.deleted_at < ?) OR (a.deleted_at <> '' AND a.deleted_at < ?)`, before, before, before)
	if err != nil {
		return nil, err
	}
	files, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`DELETE FROM attachments WHERE deleted_at <> '' AND deleted_at < ?`,
		`DELETE FROM cards WHERE deleted_at <> '' AND deleted_at < ?`,
		`DELETE FROM lists WHERE deleted_at <> '' AND deleted_at < ?`,
		`DELETE FROM boards WHERE deleted_at <> '' AND deleted_at < ?`,
	} {
		if _, err := s.DB.ExecContext(ctx, q, before); err != nil {
			return files, err
		}
	}
	return files, nil
}

// ---- comments ----

const commentCols = `c.id, c.card_id, c.user_id, COALESCE(u.name, u.email, ''), c.parent_id, c.message, c.source, c.external_id, c.created_at, c.updated_at`

func scanComment(row interface{ Scan(...any) error }) (*Comment, error) {
	var c Comment
	var uid, pid sql.NullInt64
	if err := row.Scan(&c.ID, &c.CardID, &uid, &c.AuthorName, &pid, &c.Message, &c.Source, &c.ExternalID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if uid.Valid {
		c.UserID = &uid.Int64
	}
	if pid.Valid {
		c.ParentID = &pid.Int64
	}
	return &c, nil
}

// Comments lists a card's comments, oldest first.
func (s *Store) Comments(ctx context.Context, cardID int64) ([]Comment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+commentCols+` FROM comments c LEFT JOIN users u ON u.id = c.user_id WHERE c.card_id = ? ORDER BY c.id`, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CommentByID returns a comment.
func (s *Store) CommentByID(ctx context.Context, id int64) (*Comment, error) {
	return scanComment(s.DB.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments c LEFT JOIN users u ON u.id = c.user_id WHERE c.id = ?`, id))
}

// CreateComment adds a comment by uid.
func (s *Store) CreateComment(ctx context.Context, uid int64, c *Comment) error {
	if c.Source == "" {
		c.Source = "app"
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO comments (card_id, user_id, parent_id, message, source, external_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		c.CardID, nullI(&uid), nullI(c.ParentID), c.Message, c.Source, c.ExternalID, firstNonEmpty(c.CreatedAt, now), firstNonEmpty(c.CreatedAt, now))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	nc, err := s.CommentByID(ctx, id)
	if err == nil {
		*c = *nc
	}
	return err
}

// UpdateComment changes a comment's text.
func (s *Store) UpdateComment(ctx context.Context, c *Comment) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET message=?, updated_at=? WHERE id=?`, c.Message, Now(), c.ID)
	return err
}

// DeleteComment removes a comment.
func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM comments WHERE id=?`, id)
	return err
}

// CommentExists says whether an imported comment is already there.
func (s *Store) CommentExists(ctx context.Context, cardID int64, source, ext string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM comments WHERE card_id=? AND source=? AND external_id=?`, cardID, source, ext).Scan(&n)
	return n > 0
}

// ---- attachments ----

const attCols = `id, card_id, user_id, filename, mime, size, stored, deleted_at, created_at`

func scanAttachment(row interface{ Scan(...any) error }) (*Attachment, error) {
	var a Attachment
	var uid sql.NullInt64
	if err := row.Scan(&a.ID, &a.CardID, &uid, &a.Filename, &a.Mime, &a.Size, &a.Stored, &a.DeletedAt, &a.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	if uid.Valid {
		a.UserID = &uid.Int64
	}
	return &a, nil
}

// Attachments lists a card's files.
func (s *Store) Attachments(ctx context.Context, cardID int64) ([]Attachment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+attCols+` FROM attachments WHERE card_id = ? AND deleted_at = '' ORDER BY id`, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// AttachmentByID returns a file record.
func (s *Store) AttachmentByID(ctx context.Context, id int64) (*Attachment, error) {
	return scanAttachment(s.DB.QueryRowContext(ctx, `SELECT `+attCols+` FROM attachments WHERE id = ?`, id))
}

// CreateAttachment records a stored file.
func (s *Store) CreateAttachment(ctx context.Context, uid int64, a *Attachment) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO attachments (card_id, user_id, filename, mime, size, stored, created_at) VALUES (?,?,?,?,?,?,?)`,
		a.CardID, nullI(&uid), a.Filename, a.Mime, a.Size, a.Stored, Now())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	na, err := s.AttachmentByID(ctx, id)
	if err == nil {
		*a = *na
	}
	return err
}

// TrashAttachment deletes a file record (restorable a while, Deck-style).
func (s *Store) TrashAttachment(ctx context.Context, id int64, trashed bool) error {
	v := ""
	if trashed {
		v = Now()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE attachments SET deleted_at=? WHERE id=?`, v, id)
	return err
}

// ---- activity ----

// AddActivity records what happened on a board.
func (s *Store) AddActivity(ctx context.Context, boardID int64, cardID *int64, uid int64, kind string, detail map[string]any, via string) {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO activity (board_id, card_id, user_id, kind, detail, via) VALUES (?,?,?,?,?,?)`, boardID, nullI(cardID), nullI(&uid), kind, string(b), via)
}

// ListActivity returns a board's (or one card's) activity, newest first.
func (s *Store) ListActivity(ctx context.Context, boardID int64, cardID int64, before int64, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT a.id, a.board_id, a.card_id, a.user_id, COALESCE(u.name, u.email, ''), a.kind, a.detail, a.via, a.created_at FROM activity a LEFT JOIN users u ON u.id = a.user_id WHERE a.board_id = ?`
	args := []any{boardID}
	if cardID > 0 {
		q += ` AND a.card_id = ?`
		args = append(args, cardID)
	}
	if before > 0 {
		q += ` AND a.id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var cid, uid sql.NullInt64
		var detail string
		if err := rows.Scan(&a.ID, &a.BoardID, &cid, &uid, &a.UserName, &a.Kind, &detail, &a.Via, &a.CreatedAt); err != nil {
			return nil, err
		}
		if cid.Valid {
			a.CardID = &cid.Int64
		}
		if uid.Valid {
			a.UserID = &uid.Int64
		}
		a.Detail = json.RawMessage(detail)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- helpers ----

func (s *Store) renumber(ctx context.Context, table string, ids []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET position = ? WHERE id = ?`, i, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// insertAt inserts id into ids at position (out of range or -1 = at the end).
func insertAt(ids []int64, id int64, position int) []int64 {
	if position < 0 || position > len(ids) {
		position = len(ids)
	}
	out := make([]int64, 0, len(ids)+1)
	out = append(out, ids[:position]...)
	out = append(out, id)
	return append(out, ids[position:]...)
}

func scanIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- login flow (Nextcloud Login Flow v2 for Deck clients) ----

// LoginFlow is a pending client sign-in.
type LoginFlow struct {
	Token       string
	LoginToken  string
	UserID      *int64
	AppPassword string
	ClientName  string
	ExpiresAt   string
}

// CreateLoginFlow starts one (token stored hashed).
func (s *Store) CreateLoginFlow(ctx context.Context, pollToken, loginToken, client string, ttl time.Duration) error {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_flows WHERE expires_at < ?`, Now())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO login_flows (token, login_token, client_name, expires_at) VALUES (?,?,?,?)`,
		HashToken(pollToken), loginToken, client, time.Now().UTC().Add(ttl).Format(time.RFC3339Nano))
	return err
}

func (s *Store) loginFlow(ctx context.Context, where string, arg any) (*LoginFlow, error) {
	var f LoginFlow
	var uid sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT token, login_token, user_id, app_password, client_name, expires_at FROM login_flows WHERE `+where+` AND expires_at > ?`, arg, Now()).
		Scan(&f.Token, &f.LoginToken, &uid, &f.AppPassword, &f.ClientName, &f.ExpiresAt)
	if err != nil {
		return nil, notFound(err)
	}
	if uid.Valid {
		f.UserID = &uid.Int64
	}
	return &f, nil
}

// LoginFlowByLogin finds a pending flow by the token in the browser URL.
func (s *Store) LoginFlowByLogin(ctx context.Context, loginToken string) (*LoginFlow, error) {
	return s.loginFlow(ctx, `login_token = ?`, loginToken)
}

// ApproveLoginFlow attaches the person and the app password made for the client.
func (s *Store) ApproveLoginFlow(ctx context.Context, loginToken string, uid int64, appPassword string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE login_flows SET user_id=?, app_password=? WHERE login_token=? AND user_id IS NULL AND expires_at > ?`, uid, appPassword, loginToken, Now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PollLoginFlow hands out the app password once (then the flow is gone).
func (s *Store) PollLoginFlow(ctx context.Context, pollToken string) (*LoginFlow, error) {
	f, err := s.loginFlow(ctx, `token = ?`, HashToken(pollToken))
	if err != nil {
		return nil, err
	}
	if f.UserID == nil || f.AppPassword == "" {
		return nil, ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_flows WHERE token = ?`, f.Token)
	return f, nil
}
