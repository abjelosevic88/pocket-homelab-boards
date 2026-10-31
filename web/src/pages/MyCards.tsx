// My cards: everything assigned to me (or everything with a date), by when it is due.
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { dayDiff } from '../lib/format'
import type { Card } from '../lib/types'
import { useCrumb, useOnChange } from '../components/Layout'
import { CardFace } from '../components/bits'
import { Empty, Loading, PageHeader, Segmented, useAsync } from '../components/ui'

const GROUPS: { key: string; title: string }[] = [
  { key: 'overdue', title: 'Overdue' }, { key: 'today', title: 'Today' }, { key: 'week', title: 'Next 7 days' }, { key: 'later', title: 'Later' }, { key: 'none', title: 'No date' },
]
function groupOf(c: Card): string {
  if (!c.due_at) return 'none'
  if (new Date(c.due_at).getTime() < Date.now()) return 'overdue'
  const d = dayDiff(c.due_at)
  if (d <= 0) return 'today'
  if (d <= 7) return 'week'
  return 'later'
}

export default function MyCards() {
  useCrumb(undefined)
  const navigate = useNavigate()
  const [scope, setScope] = useState<'mine' | 'all'>(() => (localStorage.getItem('pbo-my-scope') as 'mine' | 'all') || 'mine')
  const [done, setDone] = useState(false)
  const { data, loading, reload } = useAsync(() => api.get<Card[]>(`${V1}/cards?assigned=${scope === 'mine' ? 1 : 0}&due=all${done ? '&done=1' : ''}&limit=500`), [scope, done])
  useOnChange(reload)
  const set = (s: 'mine' | 'all') => { setScope(s); localStorage.setItem('pbo-my-scope', s) }
  const cards = (data || []).filter(c => scope === 'mine' || c.due_at)
  const groups = GROUPS.map(g => ({ ...g, cards: cards.filter(c => groupOf(c) === g.key) })).filter(g => g.cards.length)
  return (
    <div className="stack loose">
      <PageHeader title="My cards" sub={scope === 'mine' ? 'Cards assigned to you, on every board.' : 'Every card with a due date on your boards.'}
        actions={<>
          <Segmented size="sm" value={scope} onChange={set} options={[{ value: 'mine', label: 'Assigned to me' }, { value: 'all', label: 'Everything due' }]} />
          <Segmented size="sm" value={done ? 'done' : 'open'} onChange={v => setDone(v === 'done')} options={[{ value: 'open', label: 'Open' }, { value: 'done', label: 'With done' }]} />
        </>} />
      {loading && !data ? <Loading /> : groups.length === 0 ? (
        <Empty icon="check-circle" title={scope === 'mine' ? 'Nothing assigned to you' : 'Nothing with a due date'}>
          {scope === 'mine' ? 'Open a card and add yourself under Members, or type @yourname when adding one.' : 'Give a card a due date and it shows up here — and in the calendar feed.'}
        </Empty>
      ) : groups.map(g => (
        <section key={g.key}>
          <div className="sec-head"><h2 className={g.key === 'overdue' ? 'bad' : ''}>{g.title}</h2><span className="badge">{g.cards.length}</span></div>
          <div className="card-list">
            {g.cards.map(c => (
              <button key={c.id} type="button" className="cardface row-card" onClick={() => navigate(`/b/${c.board_id}/c/${c.id}`)}>
                <CardFace card={c} labels={[]} members={[]} showBoard />
              </button>
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}
