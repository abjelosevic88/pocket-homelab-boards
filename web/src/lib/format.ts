export function todayISO(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
export function addDays(iso: string, n: number): string {
  const [y, m, d] = iso.split('-').map(Number)
  const t = new Date(y, m - 1, d + n)
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`
}
export function daysBetween(a: string, b: string): number {
  if (!a || !b) return 0
  const [y1, m1, d1] = a.split('-').map(Number), [y2, m2, d2] = b.split('-').map(Number)
  return Math.round((Date.UTC(y2, m2 - 1, d2) - Date.UTC(y1, m1 - 1, d1)) / 86400000)
}
/** Every date from a to b inclusive. */
export function dateRange(a: string, b: string): string[] {
  const out: string[] = []
  if (!a || !b || b < a) return out
  for (let d = a, i = 0; d <= b && i < 400; d = addDays(d, 1), i++) out.push(d)
  return out
}
export function weekday(iso: string, long = false): string {
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { weekday: long ? 'long' : 'short' })
}

// ---- dates: the format comes from Settings → General and is set once by the app context.
export type DateFormat = 'auto' | 'dmy-dot' | 'dmy-slash' | 'mdy-slash' | 'ymd' | 'dmmmy' | 'mmmdy'
export type TimeFormat = 'auto' | '24h' | '12h'
let DATE_FMT: DateFormat = 'auto'
let TIME_FMT: TimeFormat = 'auto'
export function setDateFormats(d: DateFormat, t: TimeFormat) { DATE_FMT = d; TIME_FMT = t }
export const DATE_FORMATS: { id: DateFormat; label: string }[] = [
  { id: 'auto', label: 'Browser locale' }, { id: 'dmy-dot', label: '31.12.2026' }, { id: 'dmy-slash', label: '31/12/2026' },
  { id: 'mdy-slash', label: '12/31/2026' }, { id: 'ymd', label: '2026-12-31' }, { id: 'dmmmy', label: '31 Dec 2026' }, { id: 'mmmdy', label: 'Dec 31, 2026' },
]
export const TIME_FORMATS: { id: TimeFormat; label: string }[] = [{ id: 'auto', label: 'Browser locale' }, { id: '24h', label: '17:05' }, { id: '12h', label: '5:05 PM' }]
const MON = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
export function fmtYMD(y: number, m: number, d: number, withYear = true, fmt: DateFormat = DATE_FMT): string {
  const dd = String(d).padStart(2, '0'), mm = String(m).padStart(2, '0')
  switch (fmt) {
    case 'dmy-dot': return withYear ? `${dd}.${mm}.${y}` : `${dd}.${mm}.`
    case 'dmy-slash': return withYear ? `${dd}/${mm}/${y}` : `${dd}/${mm}`
    case 'mdy-slash': return withYear ? `${mm}/${dd}/${y}` : `${mm}/${dd}`
    case 'ymd': return withYear ? `${y}-${mm}-${dd}` : `${mm}-${dd}`
    case 'dmmmy': return withYear ? `${d} ${MON[m - 1]} ${y}` : `${d} ${MON[m - 1]}`
    case 'mmmdy': return withYear ? `${MON[m - 1]} ${d}, ${y}` : `${MON[m - 1]} ${d}`
    default: return new Date(y, m - 1, d).toLocaleDateString(undefined, withYear ? { day: 'numeric', month: 'short', year: 'numeric' } : { day: 'numeric', month: 'short' })
  }
}
export function fmtDate(d: string, withYear = true): string {
  if (!d) return ''
  const [y, m, day] = d.slice(0, 10).split('-').map(Number)
  return fmtYMD(y, m, day, withYear)
}
/** "14 Aug – 28 Aug 2026" */
export function fmtRange(a: string, b: string): string {
  if (!a && !b) return 'no dates yet'
  if (!b || a === b) return fmtDate(a || b)
  const sameYear = a.slice(0, 4) === b.slice(0, 4)
  return `${fmtDate(a, !sameYear)} – ${fmtDate(b)}`
}
export function fmtTime(t: Date, fmt: TimeFormat = TIME_FMT): string {
  const h = t.getHours(), mi = String(t.getMinutes()).padStart(2, '0')
  switch (fmt) {
    case '24h': return `${String(h).padStart(2, '0')}:${mi}`
    case '12h': return `${h % 12 || 12}:${mi} ${h < 12 ? 'AM' : 'PM'}`
    default: return t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }
}
/** "HH:MM" wall time → formatted. */
export function fmtClock(hhmm: string): string {
  if (!hhmm || hhmm.length < 5) return ''
  const t = new Date(2000, 0, 1, Number(hhmm.slice(0, 2)), Number(hhmm.slice(3, 5)))
  return fmtTime(t)
}
/** "2026-08-28T07:30" (wall) or ISO → "28 Aug 2026 07:30". */
export function fmtDateTime(s: string, withYear = true): string {
  if (!s) return ''
  if (s.length === 16 && s[10] === 'T') return `${fmtDate(s, withYear)} ${fmtClock(s.slice(11))}`
  const t = new Date(s)
  if (Number.isNaN(t.getTime())) return s
  return `${fmtYMD(t.getFullYear(), t.getMonth() + 1, t.getDate(), withYear)} ${fmtTime(t)}`
}
export function fmtISOTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? '' : fmtTime(t)
}
export function ago(iso: string): string {
  if (!iso) return 'never'
  const t = new Date(iso).getTime()
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  if (s < 86400 * 7) return `${Math.floor(s / 86400)} d ago`
  return fmtDateTime(iso)
}
export function fmtDuration(min: number): string {
  if (!min) return ''
  const h = Math.floor(min / 60), m = min % 60
  return h ? (m ? `${h} h ${m} min` : `${h} h`) : `${m} min`
}
export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
export function plural(n: number, one: string, many = one + 's'): string { return `${n.toLocaleString()} ${n === 1 ? one : many}` }
export function initials(name: string): string {
  const p = name.replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean)
  return ((p[0]?.[0] || '?') + (p[1]?.[0] || '')).toUpperCase()
}

// ---- due dates ----

function startOfDay(d: Date) { return new Date(d.getFullYear(), d.getMonth(), d.getDate()) }
/** Days from today to the date (negative = past). */
export function dayDiff(iso: string): number {
  const t = new Date(iso)
  return Math.round((startOfDay(t).getTime() - startOfDay(new Date()).getTime()) / 86400000)
}
/** "Today 17:00", "Tomorrow", "Yesterday", "Fri", "3 Nov", "3 Nov 2027". */
export function fmtDue(iso: string, withTime = true): string {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return iso
  const dd = dayDiff(iso)
  const time = withTime && !(t.getHours() === 0 && t.getMinutes() === 0) ? ` ${fmtTime(t)}` : ''
  if (dd === 0) return `Today${time}`
  if (dd === 1) return `Tomorrow${time}`
  if (dd === -1) return `Yesterday${time}`
  if (dd > 1 && dd < 7) return t.toLocaleDateString(undefined, { weekday: 'short' }) + time
  return fmtYMD(t.getFullYear(), t.getMonth() + 1, t.getDate(), t.getFullYear() !== new Date().getFullYear())
}
export type DueState = '' | 'done' | 'overdue' | 'soon' | 'later'
export function dueState(due: string, done: boolean): DueState {
  if (!due) return ''
  if (done) return 'done'
  const ms = new Date(due).getTime() - Date.now()
  if (ms < 0) return 'overdue'
  if (ms < 48 * 3600 * 1000) return 'soon'
  return 'later'
}
/** Local "YYYY-MM-DD" and "HH:MM" of an ISO time. */
export function splitLocal(iso: string): { date: string; time: string } {
  if (!iso) return { date: '', time: '' }
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return { date: '', time: '' }
  const p = (n: number) => String(n).padStart(2, '0')
  return { date: `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())}`, time: `${p(t.getHours())}:${p(t.getMinutes())}` }
}
/** Local date + time → RFC 3339 with the browser's offset. */
export function joinLocal(date: string, time: string): string {
  if (!date) return ''
  const [y, m, d] = date.split('-').map(Number)
  const [hh, mm] = (time || '00:00').split(':').map(Number)
  const t = new Date(y, m - 1, d, hh || 0, mm || 0)
  const off = -t.getTimezoneOffset()
  const p = (n: number) => String(Math.abs(n)).padStart(2, '0')
  return `${y}-${p(m)}-${p(d)}T${p(hh || 0)}:${p(mm || 0)}:00${off >= 0 ? '+' : '-'}${p(Math.trunc(off / 60))}:${p(off % 60)}`
}

/** 5400 → "1h 30m", 600 → "10m". */
export function fmtSpent(sec: number): string {
  const m = Math.round(sec / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60), r = m % 60
  return r ? `${h}h ${r}m` : `${h}h`
}
