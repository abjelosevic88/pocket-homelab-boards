// Home: what is due, starred and recent boards, every board.
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Board, Card } from '../lib/types'
import { announceChange, openCreate, useHome } from '../components/Layout'
import { BoardTile, CardFace } from '../components/bits'
import { Icon } from '../components/icons'
import { Empty, Loading, PageHeader, useAsync, useToast } from '../components/ui'

export default function Home() {
  const { home } = useHome()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [showArchived, setShowArchived] = useState(false)
  const archived = useAsync(() => showArchived ? api.get<Board[]>(`${V1}/boards?archived=1`) : Promise.resolve([] as Board[]), [showArchived])
  if (!home) return <Loading />
  const live = home.boards.filter(b => !b.archived)
  const byId = new Map(home.boards.map(b => [b.id, b]))
  const starred = live.filter(b => b.starred)
  const recent = home.recent.map(id => byId.get(id)).filter((b): b is Board => !!b && !b.archived && !b.starred).slice(0, 4)
  const star = async (b: Board) => { try { await api.put(`${V1}/boards/${b.id}`, { starred: !b.starred }); announceChange() } catch (e) { toast((e as Error).message, 'err') } }
  const unarchive = async (b: Board) => { try { await api.put(`${V1}/boards/${b.id}`, { archived: false }); archived.reload(); announceChange(); toast('Board is back') } catch (e) { toast((e as Error).message, 'err') } }
  const c = home.counts
  return (
    <div className="stack loose">
      <PageHeader title="Home" sub={c.overdue || c.today ? <>{c.overdue > 0 && <span className="bad">{c.overdue} overdue</span>}{c.overdue > 0 && c.today > 0 && ' · '}{c.today > 0 && `${c.today} due today`}{c.week > 0 && ` · ${c.week} this week`}</> : live.length ? 'Nothing due today.' : undefined}
        actions={<button type="button" className="btn primary" onClick={() => openCreate('blank')}><Icon name="plus" size={15} />New board</button>} />

      {home.due.length > 0 && (
        <section>
          <div className="sec-head"><h2>Coming up</h2><button type="button" className="link-btn" onClick={() => navigate('/my')}>All my cards</button></div>
          <div className="due-strip">
            {home.due.slice(0, 12).map((card: Card) => (
              <button key={card.id} type="button" className="cardface mini" onClick={() => navigate(`/b/${card.board_id}/c/${card.id}`)}>
                <CardFace card={card} labels={[]} members={[]} showBoard />
              </button>
            ))}
          </div>
        </section>
      )}

      {starred.length > 0 && <section><div className="sec-head"><h2><Icon name="star" size={15} />Starred</h2></div><div className="btiles">{starred.map(b => <BoardTile key={b.id} board={b} onStar={star} />)}</div></section>}
      {recent.length > 0 && <section><div className="sec-head"><h2><Icon name="clock" size={15} />Recently opened</h2></div><div className="btiles">{recent.map(b => <BoardTile key={b.id} board={b} onStar={star} />)}</div></section>}

      <section>
        <div className="sec-head"><h2>All boards</h2><button type="button" className="link-btn" onClick={() => setShowArchived(s => !s)}>{showArchived ? 'Hide archived' : 'Show archived'}</button></div>
        {live.length === 0 ? (
          <Empty icon="grid" title="No boards yet">
            <p>A board holds lists, and lists hold cards. Start blank, from a template, or bring your boards over from Nextcloud Deck or Trello.</p>
            <div className="row wrap" style={{ justifyContent: 'center', marginTop: 10 }}>
              <button type="button" className="btn primary" onClick={() => openCreate('blank')}><Icon name="plus" size={15} />New board</button>
              <button type="button" className="btn" onClick={() => openCreate('template')}><Icon name="layers" size={15} />From a template</button>
              <button type="button" className="btn" onClick={() => openCreate('import')}><Icon name="upload" size={15} />Import</button>
            </div>
          </Empty>
        ) : (
          <div className="btiles">
            {live.map(b => <BoardTile key={b.id} board={b} onStar={star} />)}
            <button type="button" className="btile add" onClick={() => openCreate('blank')}><Icon name="plus" size={18} />New board</button>
          </div>
        )}
      </section>

      {showArchived && (
        <section>
          <div className="sec-head"><h2><Icon name="archive" size={15} />Archived boards</h2></div>
          {archived.loading ? <Loading /> : (archived.data || []).length === 0 ? <p className="muted">No archived boards.</p> : (
            <div className="arch-list">
              {(archived.data || []).map(b => (
                <div key={b.id} className="arch-row">
                  <span className="sb-board-dot" style={{ background: `#${b.color}` }}>{b.emoji}</span>
                  <button type="button" className="link-btn grow ellipsis" onClick={() => navigate(`/b/${b.id}`)}>{b.title}</button>
                  {b.role !== 'viewer' && <button type="button" className="btn sm" onClick={() => unarchive(b)}>Unarchive</button>}
                </div>
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  )
}
