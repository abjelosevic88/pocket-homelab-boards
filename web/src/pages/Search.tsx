// Search: boards and cards matching the words, across every board you can see.
import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { SearchResult } from '../lib/types'
import { BoardTile, CardFace } from '../components/bits'
import { Icon } from '../components/icons'
import { Empty, Loading, PageHeader, useAsync, useDebounce } from '../components/ui'

export default function Search() {
  const [sp, setSp] = useSearchParams()
  const navigate = useNavigate()
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q.trim(), 250)
  useEffect(() => { setSp(dq ? { q: dq } : {}, { replace: true }) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const { data, loading } = useAsync(() => dq ? api.get<SearchResult>(`${V1}/search?q=${encodeURIComponent(dq)}`) : Promise.resolve({ boards: [], cards: [] } as SearchResult), [dq])
  return (
    <div className="stack loose">
      <PageHeader title="Search" />
      <div className="search-box"><Icon name="search" size={16} /><input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Words in a card’s title or description, or a board’s name" aria-label="Search" /></div>
      {!dq ? <p className="muted">Type to search every board you can see.</p> : loading && !data ? <Loading /> : (data && data.boards.length + data.cards.length === 0) ? <Empty icon="search" title="Nothing found">Try fewer words.</Empty> : data && <>
        {data.boards.length > 0 && <section><div className="sec-head"><h2>Boards</h2></div><div className="btiles">{data.boards.map(b => <BoardTile key={b.id} board={b} />)}</div></section>}
        {data.cards.length > 0 && <section><div className="sec-head"><h2>Cards</h2><span className="badge">{data.cards.length}</span></div><div className="card-list">
          {data.cards.map(c => <button key={c.id} type="button" className="cardface row-card" onClick={() => navigate(`/b/${c.board_id}/c/${c.id}`)}><CardFace card={c} labels={[]} members={[]} showBoard /></button>)}
        </div></section>}
      </>}
    </div>
  )
}
