export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number
  default_view: 'home' | 'mine'; card_density: 'comfortable' | 'compact'; show_done_cards: boolean; confirm_delete: boolean; due_time_default: string
  notify_due: boolean; remind_before_min: number; notify_assigned: boolean; notify_comments: boolean; notify_digest: boolean; digest_hour: number
  notify_url: string; notify_format: string; notify_configured: boolean
  ics_url: string
  tasks_mode: ConnMode; tasks_via: ConnVia; tasks_detail: string; tasks_open_url: string; tasks_configured: boolean
  tasks_url: string; tasks_token: string; tasks_public_url: string
  time_mode: ConnMode; time_via: ConnVia; time_detail: string; time_open_url: string; time_configured: boolean
  time_url: string; time_token: string; time_public_url: string
  nextcloud_mode: ConnMode; nextcloud_url: string; nextcloud_user: string; nextcloud_password: string
  nextcloud_via: ConnVia; nextcloud_detail: string; nextcloud_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string
}

export type Role = 'owner' | 'editor' | 'viewer'

export interface Board {
  id: number; owner_id: number; title: string; description: string; color: string; emoji: string; archived: boolean
  source: string; external_id: string; deleted_at: string; created_at: string; updated_at: string
  role: Role; starred: boolean; position: number; opened_at: string; shared: boolean
  cards: number; open: number; overdue: number; due_soon: number; assigned: number
}

export interface Member { user_id: number; name: string; email: string; role: Role }

export interface List {
  id: number; board_id: number; title: string; position: number; wip_limit: number; is_done: boolean; color: string
  external_id: string; deleted_at: string; created_at: string; updated_at: string
  cards?: Card[]
}

export interface Label { id: number; board_id: number; title: string; color: string; position: number }

export type Repeat = '' | 'daily' | 'weekdays' | 'weekly' | 'monthly' | 'yearly'

export interface Card {
  id: number; board_id: number; list_id: number; title: string; description: string; position: number
  start_at: string; due_at: string; done: boolean; done_at: string; archived: boolean; cover_color: string; repeat: Repeat
  created_by: number | null; updated_by: number | null; source: string; external_id: string; task_ref: string
  deleted_at: string; created_at: string; updated_at: string
  labels: number[]; members: number[]; comments: number; attachments: number; checklist_done: number; checklist_total: number
  board_title?: string; board_color?: string; list_title?: string
}

export interface Comment { id: number; card_id: number; user_id: number | null; author_name: string; parent_id: number | null; message: string; source: string; created_at: string; updated_at: string }
export interface Attachment { id: number; card_id: number; user_id: number | null; filename: string; mime: string; size: number; created_at: string }
export interface Activity { id: number; board_id: number; card_id: number | null; user_id: number | null; user_name: string; kind: string; detail: Record<string, unknown> | null; via: string; created_at: string }

export interface BoardView { board: Board; lists: List[]; labels: Label[]; members: Member[] }
export interface WorklogEntry { id?: number; duration_s: number; date: string; source: string; note?: string }
export interface CardView { card: Card; board: Board; list: List; labels: Label[]; members: Member[]; comments: Comment[]; attachments: Attachment[]; activity: Activity[]; worklog?: WorklogEntry[]; time_spent_s?: number }
export interface RunningTimer { id: number; title: string; app: string; app_ref: string; app_label: string; source: string; started_at: string }
export interface TimerState { connected: boolean; running: RunningTimer | null; url: string }
export interface TimerResult { ok: boolean; url?: string; error?: string; conflict?: boolean; running?: RunningTimer }

export interface HomeData {
  boards: Board[]; starred: number[]; recent: number[]; due: Card[]
  counts: { overdue: number; today: number; week: number; assigned: number }
}

export interface TemplateList { title: string; is_done?: boolean; cards: string[] }
export interface Template { key?: string; title: string; description?: string; emoji: string; color: string; lists: TemplateList[]; labels: { title: string; color: string }[] }

export interface Person { id: number; name: string; email: string }
export type TrashList = List & { board_title: string; board_color: string; card_count: number }
export interface Trash { boards: Board[]; lists?: TrashList[]; cards: Card[] }
export interface SearchResult { boards: Board[]; cards: Card[] }
export interface AIStatus { ready: boolean; provider: string; detail: string }
export interface DeckBoard { id: number; title: string; color: string; archived: boolean; imported_board_id: number | null }
