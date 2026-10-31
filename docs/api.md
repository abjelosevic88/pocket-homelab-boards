# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie, a personal token (`Authorization: Bearer pbo_…`, Settings → API & shortcuts), a Pocket Core token, or a Pocket Core link token (`pcx_…`, only the routes of its scope). Errors are `{"error": "…"}`.

**Who sees what.** A board belongs to the person who made it. They can share it with other people on this box as *editor* (change everything except sharing) or *viewer* (read only). Everything inside a board — lists, cards, labels, comments, files, activity — is reached through the board: anyone who can't see the board gets **404** for all of it, and viewers get **403** when they try to change something. Admins see nobody else's boards.

There is also a [Nextcloud Deck–compatible API](#nextcloud-deck-compatible-api) for Deck clients and scripts.

## Objects

**Board** — `id, owner_id, title, description, color` (hex, no `#`)`, emoji, archived, source, external_id, deleted_at, created_at, updated_at`, plus the caller's own view: `role` (`owner|editor|viewer`), `starred, position, opened_at, shared`, and in lists the counters `cards, open, overdue, due_soon, assigned`.

**List** — `id, board_id, title, position, wip_limit` (0 = none)`, is_done` (cards moved here are marked done)`, color, external_id, deleted_at, created_at, updated_at`.

**Label** — `id, board_id, title, color, position`.

**Card** — `id, board_id, list_id, title, description` (Markdown; its task lines `- [ ]` / `- [x]` are the checklist)`, position, start_at, due_at` (RFC 3339 or `""`)`, done, done_at, archived, cover_color, repeat` (`""|daily|weekdays|weekly|monthly|yearly`)`, created_by, updated_by, source, external_id, task_ref, deleted_at, created_at, updated_at, labels` (ids)`, members` (user ids)`, comments, attachments` (counts)`, checklist_done, checklist_total`. Across boards also `board_title, board_color, list_title`.

**Member** — `user_id, name, email, role`. **Comment** — `id, card_id, user_id, author_name, parent_id, message, source, created_at, updated_at`. **Attachment** — `id, card_id, user_id, filename, mime, size, created_at`. **Activity** — `id, board_id, card_id, user_id, user_name, kind, detail, via, created_at`.

## Home and search

| Method | Path | |
|---|---|---|
| GET | `/home` | `{boards: Board[] (with counters), starred: id[], recent: id[] (last opened, 6), due: Card[] (mine or unassigned on my boards, overdue + next 7 days), counts: {overdue, today, week, assigned}}` |
| GET | `/cards?assigned=1&due=overdue\|today\|week\|later\|none\|all&q=&board=&done=1&limit=` | cards across every board I can see ("My cards", default `assigned=1&due=all`) |
| GET | `/search?q=` | `{boards: Board[], cards: Card[]}` |
| GET | `/people` | people on this box I can share with: `[{id, name, email}]` |
| GET | `/templates` | board templates: `[{key, title, description, emoji, color, lists: [{title, is_done, cards: [string]}], labels: [{title, color}]}]` |

## Boards

| Method | Path | |
|---|---|---|
| GET | `/boards?archived=0\|1` | Board[] with counters (both when omitted) |
| POST | `/boards` | `{title, description?, color?, emoji?, template?, template_data?}` → Board. Without a template: lists "To do", "Doing", "Done" (done list) and six default labels |
| GET | `/boards/{id}` | `{board, lists: List[] (each with cards: Card[]), labels: Label[], members: Member[]}`; `?archived=1` returns the archived cards instead. Marks the board opened |
| PUT | `/boards/{id}` | any of `title, description, color, emoji, archived` (editor+), `starred, position` (anyone, personal) |
| DELETE | `/boards/{id}` | owner: to the bin (restorable 30 days); `?purge=1` for good. A member calling DELETE leaves the board |
| POST | `/boards/{id}/restore` | owner: out of the bin |
| POST | `/boards/{id}/copy` | `{title, cards: bool}` → Board (labels and lists always, cards when asked) |
| GET | `/boards/{id}/activity?card=&before=&limit=` | Activity[] |
| GET | `/boards/{id}/events` | Server-Sent Events: `event: changed` with `data: {"board_id":1,"by":2,"kind":"card_moved","card_id":5}` whenever someone changes the board (refetch), `: ping` every 25 s |
| GET | `/boards/{id}/members` | Member[] |
| POST | `/boards/{id}/members` | owner: `{user_id or email, role}` → Member[] |
| PUT | `/boards/{id}/members/{uid}` | owner: `{role}` |
| DELETE | `/boards/{id}/members/{uid}` | owner removes someone, or a member removes themselves |
| GET | `/trash` | `{boards: Board[], lists: (List + board_title, board_color, card_count)[], cards: Card[]}` in my bin |

## Lists

| Method | Path | |
|---|---|---|
| POST | `/boards/{id}/lists` | `{title, position? (-1 end), wip_limit?, is_done?}` → List |
| PUT | `/lists/{id}` | any of `title, wip_limit, is_done, color, position` |
| DELETE | `/lists/{id}` | to the bin with its cards; `?purge=1` for good |
| POST | `/lists/{id}/restore` | out of the bin (at the end of the board, with its cards) |
| POST | `/lists/{id}/archive-cards` | archive every card in the list → `{archived: n}` |
| POST | `/lists/{id}/sort` | `{by: "due"\|"title"\|"created"}` reorder the cards |

## Labels

| Method | Path | |
|---|---|---|
| POST | `/boards/{id}/labels` | `{title, color}` → Label |
| PUT | `/labels/{id}` | `{title?, color?, position?}` |
| DELETE | `/labels/{id}` | |

## Cards

| Method | Path | |
|---|---|---|
| POST | `/lists/{id}/cards` | `{title, description?, due_at?, start_at?, position? (-1 bottom, 0 top), labels?: id[], members?: id[], cover_color?, repeat?, quick?: bool}` → Card. With `quick: true` the title is read for `#label`, `@person` and `!due` (`!today`, `!tomorrow`, `!fri`, `!2026-11-03`, `!3d`, `!17:00`) |
| GET | `/cards/{id}` | `{card, board, list, labels, members, comments, attachments, activity}` |
| PUT | `/cards/{id}` | any of `title, description, due_at, start_at, done, archived, cover_color, repeat, list_id, position, board_id` (moving to another board needs `list_id` on it) |
| DELETE | `/cards/{id}` | to the bin; `?purge=1` for good |
| POST | `/cards/{id}/restore` | out of the bin (back at the bottom of its list) |
| POST | `/cards/{id}/copy` | `{title?, list_id?, position?}` → Card |
| PUT / DELETE | `/cards/{id}/labels/{labelId}` | put a label on / take it off |
| PUT / DELETE | `/cards/{id}/members/{userId}` | assign / unassign (the person must see the board) |
| PUT | `/cards/{id}/checklist/{index}` | `{done: bool}` ticks the index-th task line of the description |
| GET / POST | `/cards/{id}/comments` | `{message, parent_id?}` |
| PUT / DELETE | `/comments/{id}` | the author only |
| GET | `/cards/{id}/attachments` | Attachment[] |
| POST | `/cards/{id}/attachments` | multipart `file` → Attachment |
| GET | `/attachments/{id}` | the file (`?download=1` as attachment) |
| DELETE | `/attachments/{id}` | |

Moving a card into a done list marks it done; moving it out marks it not done. Marking a repeating card done puts the next one (next due date, checklist unticked) at the bottom of the list it was in before (or the first list that isn't a done list); the repeat moves to the new card.

## Pocket apps and AI

| Method | Path | |
|---|---|---|
| POST | `/cards/{id}/task` | send the card to Pocket Tasks → `{ok, url}` |
| POST | `/cards/{id}/timer` | start the Pocket Time timer for the card → `{ok, url}` |
| POST | `/ai/checklist` | `{card_id}` → `{items: string[]}` suggested steps (not saved) |
| POST | `/ai/board` | `{goal}` → `{title, emoji, color, lists: [{title, is_done, cards: [string]}], labels: []}` a draft board (not saved; create it with `POST /boards` + `template_data`) |
| GET | `/calendar.ics?token=` | due cards as a calendar feed (the token is in Settings → Calendar feed; no login) |
| POST | `/settings/ics-token` | make a new feed address (the old one stops working) → `{ics_url}` |

## Import and export

| Method | Path | |
|---|---|---|
| GET | `/import/deck/boards` | your Nextcloud Deck boards (via Pocket Core's Nextcloud service or Settings) → `[{id, title, color, archived, imported_board_id}]` |
| POST | `/import/deck` | `{board_ids: [], comments: true, attachments: true}` → `{boards, lists, cards, comments, attachments, skipped}`; running it again updates, never duplicates |
| POST | `/import/trello` | a Trello board export (JSON body or multipart `file`) → counts |
| GET | `/export.json` | every board you own, with lists, cards, labels, comments |
| GET | `/export.csv` | one row per card |
| POST | `/import` | an `export.json` (idempotent) |

## Overlay for other apps (`boards:read`)

`GET /overlay?from=YYYY-MM-DD&to=YYYY-MM-DD` → `{items: [{id, title, date, due_at, done, board, list, url}]}` and `GET /day/{date}` → `{date, due: Card[], done: Card[], created: Card[]}` — what Pocket Calendar or Pocket Day can show.

## Events (Pocket Core)

`boards.card_created`, `boards.card_moved`, `boards.card_done`, `boards.card_due`, `boards.card_assigned`, `boards.comment_added` — payload `{card_id, board_id, title, board, list, url, user_email, …}`.

## Nextcloud Deck–compatible API

For Deck clients (and scripts written for Deck). Served at both `/index.php/apps/deck/api/v1.0/…` and `/apps/deck/api/v1.0/…` (also `v1.1`). Sign in with HTTP Basic: your email as the user and an API token (`pbo_…`) or your password as the password, or with Nextcloud Login Flow v2 (`POST /index.php/login/v2`) which hands the client its own token after you approve it in the browser.

Supported: `boards` (list with `details`, create, get, update, delete, `undo_delete`), board `acl` (share with people on this box), `stacks` (list, archived, create, get, update, delete), `cards` (create, get, update, delete, `assignLabel`, `removeLabel`, `assignUser`, `unassignUser`, `reorder`, `archive`, `unarchive`, `done`, `undone`), `labels`, card `attachments` (list, upload, get, delete, restore), and comments under `/ocs/v2.php/apps/deck/api/v1.0/cards/{id}/comments`. Also `/status.php`, `/ocs/v2.php/cloud/capabilities` and `/ocs/v2.php/cloud/user` so clients recognise the server. `If-Modified-Since` is honoured on `boards` and `stacks`.

Mapping: Deck *stacks* are lists, `order` is position, `duedate` is `due_at`, `done` is `done_at`, `owner` / `assignedUsers` use the person's email as `uid`.
