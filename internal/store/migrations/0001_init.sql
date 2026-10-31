CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- A board belongs to the person who made it (user_id); they can share it
-- with other people on this box (board_members). Everything inside a board
-- — lists, cards, labels, comments, files — is reached through the board,
-- so whoever cannot see the board cannot see any of it.
CREATE TABLE boards (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,   -- the owner
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  color       TEXT NOT NULL DEFAULT '0c66e4',                            -- hex without #
  emoji       TEXT NOT NULL DEFAULT '',
  archived    INTEGER NOT NULL DEFAULT 0,
  source      TEXT NOT NULL DEFAULT 'app',                               -- app | deck | trello | import | <pocket app slug>
  external_id TEXT NOT NULL DEFAULT '',
  deleted_at  TEXT NOT NULL DEFAULT '',                                  -- in the bin (Deck's undo_delete)
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX boards_user ON boards(user_id, deleted_at);

-- People a board is shared with: editor (change everything but sharing) or viewer.
CREATE TABLE board_members (
  board_id   INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       TEXT NOT NULL DEFAULT 'editor',                            -- editor | viewer
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (board_id, user_id)
);
CREATE INDEX board_members_user ON board_members(user_id);

-- Each person's own view of a board they can see: star, order on the home page, last opened.
CREATE TABLE board_prefs (
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  board_id  INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  starred   INTEGER NOT NULL DEFAULT 0,
  position  INTEGER NOT NULL DEFAULT 0,
  opened_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, board_id)
);

-- Lists (Deck calls them stacks). A "done" list marks the cards moved into it done.
CREATE TABLE lists (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  board_id   INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  position   INTEGER NOT NULL DEFAULT 0,
  wip_limit  INTEGER NOT NULL DEFAULT 0,                                -- 0 = no limit
  is_done    INTEGER NOT NULL DEFAULT 0,
  color      TEXT NOT NULL DEFAULT '',
  external_id TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX lists_board ON lists(board_id, position);

CREATE TABLE labels (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  board_id   INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  title      TEXT NOT NULL DEFAULT '',
  color      TEXT NOT NULL DEFAULT '61bd4f',                            -- hex without #
  position   INTEGER NOT NULL DEFAULT 0,
  external_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX labels_board ON labels(board_id);

-- Cards. The description is Markdown; its task lines (- [ ] / - [x]) are the
-- card's checklist, exactly as Nextcloud Deck keeps it.
CREATE TABLE cards (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  board_id    INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  list_id     INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  position    INTEGER NOT NULL DEFAULT 0,
  start_at    TEXT NOT NULL DEFAULT '',                                 -- RFC3339
  due_at      TEXT NOT NULL DEFAULT '',                                 -- RFC3339
  done_at     TEXT NOT NULL DEFAULT '',                                 -- '' = not done
  archived    INTEGER NOT NULL DEFAULT 0,
  cover_color TEXT NOT NULL DEFAULT '',
  repeat      TEXT NOT NULL DEFAULT '',                                 -- '' | daily | weekdays | weekly | monthly | yearly
  created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  updated_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  source      TEXT NOT NULL DEFAULT 'app',                              -- app | deck | trello | import | <pocket app slug>
  external_id TEXT NOT NULL DEFAULT '',
  reminded_at TEXT NOT NULL DEFAULT '',                                 -- the due reminder went out for this due_at
  task_ref    TEXT NOT NULL DEFAULT '',                                 -- the Pocket Tasks task made from this card
  deleted_at  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX cards_list ON cards(list_id, position);
CREATE INDEX cards_board ON cards(board_id, deleted_at);
CREATE INDEX cards_due ON cards(due_at) WHERE due_at <> '' AND deleted_at = '';

CREATE TABLE card_labels (
  card_id  INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  label_id INTEGER NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  PRIMARY KEY (card_id, label_id)
);
CREATE INDEX card_labels_label ON card_labels(label_id);

-- Who a card is assigned to (people who can see the board).
CREATE TABLE card_members (
  card_id INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (card_id, user_id)
);
CREATE INDEX card_members_user ON card_members(user_id);

CREATE TABLE comments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  card_id    INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,          -- the author
  parent_id  INTEGER REFERENCES comments(id) ON DELETE SET NULL,
  message    TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT 'app',
  external_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX comments_card ON comments(card_id);

-- Files on a card; the bytes live in FILES_DIR/<stored>.
CREATE TABLE attachments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  card_id    INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  filename   TEXT NOT NULL,
  mime       TEXT NOT NULL DEFAULT 'application/octet-stream',
  size       INTEGER NOT NULL DEFAULT 0,
  stored     TEXT NOT NULL,
  deleted_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX attachments_card ON attachments(card_id);

-- What happened on a board, newest first in the UI.
CREATE TABLE activity (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  board_id   INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  card_id    INTEGER,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  kind       TEXT NOT NULL,                                             -- card_created | card_moved | card_done | comment | …
  detail     TEXT NOT NULL DEFAULT '{}',
  via        TEXT NOT NULL DEFAULT '',                                  -- '' = this app, deck = the Deck API, <slug> = a Pocket app
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX activity_board ON activity(board_id, id);
CREATE INDEX activity_card ON activity(card_id, id);

-- Nextcloud Login Flow v2 for Deck clients: a pending sign-in a person
-- approves in the browser; the client then polls for its app password.
CREATE TABLE login_flows (
  token       TEXT PRIMARY KEY,                                         -- the poll token (sha256 kept)
  login_token TEXT NOT NULL UNIQUE,                                     -- in the browser URL
  user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
  app_password TEXT NOT NULL DEFAULT '',                                -- handed out once, then cleared
  client_name TEXT NOT NULL DEFAULT '',
  expires_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Time spent on a card, written by Pocket Time (the worklog contract) or by hand.
CREATE TABLE worklog (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  card_id     INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  title       TEXT NOT NULL DEFAULT '',
  note        TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT '',
  ended_at    TEXT NOT NULL DEFAULT '',
  date        TEXT NOT NULL,
  duration_s  INTEGER NOT NULL DEFAULT 0,
  source      TEXT NOT NULL DEFAULT 'app',                              -- app | time | <pocket app slug>
  external_id TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX worklog_user_date ON worklog(user_id, date);
CREATE INDEX worklog_card ON worklog(card_id);
CREATE UNIQUE INDEX worklog_ext ON worklog(user_id, external_id) WHERE external_id <> '';
