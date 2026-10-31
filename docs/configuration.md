# Configuration

Environment variables; every one has a default. Personal settings (goals, sounds, reminders, connections, notifications) live in the UI per person — the `*_URL`/`*_TOKEN`/`OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in pushes, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-boards.db` | SQLite file |
| `MAX_BODY_BYTES` | `67108864` | Largest request: a file put on a card or an export being imported (64 MB) |
| `FILES_DIR` | `$DATA_DIR/files` | Where the files on cards are kept |
| `TZ` | – | Time zone for due dates, reminders and the morning digest |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `TASKS_URL`, `TASKS_TOKEN` | – | Pocket Tasks, direct connection for the owner: send a card to your task list (or use Pocket Core) |
| `TIME_URL`, `TIME_TOKEN` | – | Pocket Time, direct connection for the owner: the shared timer, time lands back on the card (or use Pocket Core) |
| `NEXTCLOUD_URL`, `NEXTCLOUD_USER`, `NEXTCLOUD_PASSWORD` | – | Nextcloud with an app password, for importing Deck boards (or your Nextcloud account in Pocket Core) |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | The reflection and questions without Pocket AI |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Seed a few lived-in boards (and a second person, mia@example.com, for the shared one) into the owner account (screenshots, trying it out) |

Commands: `pocket-boards backup [-o FILE]`, `pocket-boards reset-password [-email] [-password]`, `pocket-boards version`.
