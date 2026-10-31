// The bin: deleted boards and cards, kept 30 days.
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { ago } from '../lib/format'
import { hex } from '../lib/colors'
import type { Board, Card } from '../lib/types'
import { announceChange, useOnChange } from '../components/Layout'
import { Icon } from '../components/icons'
import { Confirm, Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'
import type { Trash as TrashT, TrashList } from '../lib/types'

export default function Trash() {
  const { toast } = useToast()
  const { data, loading, reload } = useAsync(() => api.get<TrashT>(`${V1}/trash`), [])
  useOnChange(reload)
  const [purge, setPurge] = useState<{ kind: 'board'; it: Board } | { kind: 'list'; it: TrashList } | { kind: 'card'; it: Card } | null>(null)
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); reload(); announceChange(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  if (loading && !data) return <Loading />
  const boards = data?.boards || [], cards = data?.cards || [], lists = data?.lists || []
  return (
    <div className="stack loose">
      <PageHeader title="Bin" sub="Deleted boards, lists and cards stay here for 30 days, then they are gone for good." />
      {boards.length + cards.length + lists.length === 0 ? <Empty icon="trash" title="The bin is empty" /> : <>
        {boards.length > 0 && <section>
          <div className="sec-head"><h2>Boards</h2></div>
          <div className="arch-list">{boards.map(b => (
            <div key={b.id} className="arch-row">
              <span className="sb-board-dot" style={{ background: hex(b.color) }}>{b.emoji}</span>
              <span className="grow ellipsis"><b>{b.title}</b> <span className="muted small">deleted {ago(b.deleted_at)}</span></span>
              <button type="button" className="btn sm" onClick={() => run(() => api.post(`${V1}/boards/${b.id}/restore`), 'Board restored')}><Icon name="undo" size={14} />Restore</button>
              <button type="button" className="btn ghost sm danger-text" onClick={() => setPurge({ kind: 'board', it: b })}>Delete forever</button>
            </div>))}</div>
        </section>}
        {lists.length > 0 && <section>
          <div className="sec-head"><h2>Lists</h2></div>
          <div className="arch-list">{lists.map(l => (
            <div key={l.id} className="arch-row">
              <span className="sb-board-dot" style={{ background: hex(l.board_color) }} />
              <span className="grow ellipsis"><b>{l.title}</b> <span className="muted small">{l.board_title} · {l.card_count} {l.card_count === 1 ? 'card' : 'cards'} · deleted {ago(l.deleted_at)}</span></span>
              <button type="button" className="btn sm" onClick={() => run(() => api.post(`${V1}/lists/${l.id}/restore`), 'List restored')}><Icon name="undo" size={14} />Restore</button>
              <button type="button" className="btn ghost sm danger-text" onClick={() => setPurge({ kind: 'list', it: l })}>Delete forever</button>
            </div>))}</div>
        </section>}
        {cards.length > 0 && <section>
          <div className="sec-head"><h2>Cards</h2></div>
          <div className="arch-list">{cards.map(c => (
            <div key={c.id} className="arch-row">
              <Icon name="note" size={15} className="muted" />
              <span className="grow ellipsis"><b>{c.title}</b> <span className="muted small">{c.board_title ? `${c.board_title} · ` : ''}deleted {ago(c.deleted_at)}</span></span>
              <button type="button" className="btn sm" onClick={() => run(() => api.post(`${V1}/cards/${c.id}/restore`), 'Card restored')}><Icon name="undo" size={14} />Restore</button>
              <button type="button" className="btn ghost sm danger-text" onClick={() => setPurge({ kind: 'card', it: c })}>Delete forever</button>
            </div>))}</div>
        </section>}
      </>}
      {purge && <Confirm title={`Delete “${purge.it.title}” forever?`} confirmLabel="Delete forever" message={purge.kind === 'board' ? 'The board, its lists, cards, comments and files are removed for good.' : purge.kind === 'list' ? 'The list and the cards in it are removed for good.' : 'The card, its comments and files are removed for good.'}
        onCancel={() => setPurge(null)} onConfirm={() => { const p = purge; setPurge(null); run(() => api.del(`${V1}/${p.kind === 'board' ? 'boards' : p.kind === 'list' ? 'lists' : 'cards'}/${p.it.id}?purge=1`), 'Deleted') }} />}
    </div>
  )
}
