// One board: lists side by side, cards you drag between them, the filter, sharing,
// activity, and the card dialog on top (/b/:id/c/:cardId).
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate, useParams } from 'react-router-dom'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { hex, inkOn } from '../lib/colors'
import { ago, dayDiff } from '../lib/format'
import type { Activity, Board, BoardView, Card, Label, List, Member, Person, Role } from '../lib/types'
import { announceChange, useCrumb } from '../components/Layout'
import { Avatars, CardFace, Swatches, describeActivity } from '../components/bits'
import { CardModal } from '../components/CardModal'
import { EMOJIS } from '../components/NewBoard'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { type Anchor, Avatar, Confirm, Empty, Field, Loading, Menu, type MenuItem, Modal, Popover, Segmented, rectOf, useAsync, useIsMobile, useToast } from '../components/ui'

type DueFilter = '' | 'overdue' | 'today' | 'week' | 'none'
interface Filter { q: string; labels: number[]; members: number[]; due: DueFilter; hideDone: boolean }
const emptyFilter = (hideDone: boolean): Filter => ({ q: '', labels: [], members: [], due: '', hideDone })

type Drag =
  | { kind: 'card'; id: number; from: number; over: number; index: number; w: number; h: number }
  | { kind: 'list'; id: number; index: number; w: number; h: number }

export default function BoardPage() {
  const { id: rawId, cardId } = useParams()
  const id = Number(rawId)
  const navigate = useNavigate()
  const { toast } = useToast()
  const { settings, user } = useApp()
  const mobile = useIsMobile()
  const [view, setView] = useState<BoardView | null>(null)
  const [err, setErr] = useState('')
  const [filterOpen, setFilterOpen] = useState(false)
  const [filter, setFilter] = useState<Filter>(() => emptyFilter(settings?.show_done_cards === false))
  const [panel, setPanel] = useState<'' | 'activity'>('')
  const [dialog, setDialog] = useState<'' | 'share' | 'archived' | 'about' | 'copy' | 'delete' | 'leave'>('')
  const [composer, setComposer] = useState<number | null>(null)
  const [drag, setDragState] = useState<Drag | null>(null)
  const dragRef = useRef<Drag | null>(null)
  const setDrag = (d: Drag | null) => { dragRef.current = d; setDragState(d) }
  const pending = useRef(false)
  const scroller = useRef<HTMLDivElement>(null)
  const filterInput = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    try { const v = await api.get<BoardView>(`${V1}/boards/${id}`); setView(v); setErr('') }
    catch (e) { setErr(e instanceof ApiError && e.status === 404 ? 'This board does not exist, or it is not shared with you.' : (e as Error).message) }
  }, [id])
  useEffect(() => { setView(null); setErr(''); setFilter(emptyFilter(settings?.show_done_cards === false)); load() }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  useCrumb(view?.board.title)

  // live: someone (or another tab) changed the board → refetch, at most every ~500 ms, never mid-drag
  const timer = useRef<number | null>(null)
  const reload = useCallback(() => {
    if (dragRef.current) { pending.current = true; return }
    if (timer.current) return
    timer.current = window.setTimeout(() => { timer.current = null; if (dragRef.current) { pending.current = true; return } load() }, 500)
  }, [load])
  useEffect(() => {
    if (!id) return
    let es: EventSource | null = null
    try {
      es = new EventSource(`${V1}/boards/${id}/events`)
      es.addEventListener('changed', reload)
    } catch { /* no live updates */ }
    return () => { es?.close(); if (timer.current) { clearTimeout(timer.current); timer.current = null } }
  }, [id, reload])
  const changed = useCallback(() => { load(); announceChange() }, [load])

  // shortcuts from the shell
  useEffect(() => {
    const focus = () => { setFilterOpen(true); setTimeout(() => filterInput.current?.focus(), 30) }
    const toggle = () => setFilterOpen(o => !o)
    const newCard = () => {
      if (!view || view.board.role === 'viewer' || !view.lists.length) return
      // the list in view (phones) or the first one
      let target = view.lists[0].id
      if (scroller.current) {
        const mid = window.innerWidth / 2
        for (const el of scroller.current.querySelectorAll<HTMLElement>('[data-list-col]')) {
          const r = el.getBoundingClientRect()
          if (mobile ? r.left <= mid && r.right >= mid : r.right > 0) { target = Number(el.dataset.listCol); break }
        }
      }
      setComposer(target)
    }
    window.addEventListener('pbo:filter-focus', focus)
    window.addEventListener('pbo:filter-toggle', toggle)
    window.addEventListener('pbo:new-card', newCard)
    return () => { window.removeEventListener('pbo:filter-focus', focus); window.removeEventListener('pbo:filter-toggle', toggle); window.removeEventListener('pbo:new-card', newCard) }
  }, [view, mobile])

  const board = view?.board
  const canEdit = !!board && board.role !== 'viewer'
  const labels = view?.labels || []
  const members = view?.members || []

  const visible = useCallback((c: Card) => {
    const f = filter
    if (f.hideDone && c.done) return false
    if (f.q) {
      const words = f.q.toLowerCase().split(/\s+/).filter(Boolean)
      const text = `${c.title} ${c.description}`.toLowerCase()
      if (!words.every(w => text.includes(w))) return false
    }
    if (f.labels.length && !f.labels.some(l => c.labels.includes(l))) return false
    if (f.members.length && !f.members.some(m => (m === -1 ? c.members.length === 0 : c.members.includes(m)))) return false
    if (f.due) {
      if (f.due === 'none') return !c.due_at
      if (!c.due_at || c.done) return false
      const t = new Date(c.due_at).getTime()
      if (f.due === 'overdue') return t < Date.now()
      const d = dayDiff(c.due_at)
      if (f.due === 'today') return d <= 0
      if (f.due === 'week') return d <= 7
    }
    return true
  }, [filter])
  const filterCount = filter.labels.length + filter.members.length + (filter.due ? 1 : 0) + (filter.q ? 1 : 0)

  // ---------------- drag and drop (pointer events: mouse drags at once, touch after a long press) ----------------
  const ptr = useRef<{ x: number; y: number; sx: number; sy: number; offX: number; offY: number; started: boolean; timer: number | null; kind: 'card' | 'list'; id: number; el: HTMLElement; pid: number; touch: boolean } | null>(null)
  const ghost = useRef<HTMLDivElement>(null)
  const raf = useRef<number | null>(null)
  const suppressClick = useRef(0)

  const positionGhost = () => {
    const p = ptr.current
    if (p && ghost.current) ghost.current.style.transform = `translate(${p.x - p.offX}px, ${p.y - p.offY}px) rotate(${p.kind === 'card' ? 2.5 : 1.5}deg)`
  }
  const computeTarget = () => {
    const p = ptr.current, d = dragRef.current
    if (!p || !d || !scroller.current) return
    const cols = [...scroller.current.querySelectorAll<HTMLElement>('[data-list-col]')].filter(el => Number(el.dataset.listCol) !== (d.kind === 'list' ? d.id : -1))
    if (d.kind === 'list') {
      let index = cols.length
      for (let i = 0; i < cols.length; i++) { const r = cols[i].getBoundingClientRect(); if (p.x < r.left + r.width / 2) { index = i; break } }
      if (index !== d.index) setDrag({ ...d, index })
      return
    }
    let col: HTMLElement | null = null
    let best = Infinity
    for (const c of cols) {
      const r = c.getBoundingClientRect()
      const dist = p.x < r.left ? r.left - p.x : p.x > r.right ? p.x - r.right : 0
      if (dist < best) { best = dist; col = c }
    }
    if (!col) return
    const over = Number(col.dataset.listCol)
    const cards = [...col.querySelectorAll<HTMLElement>('[data-card-id]')].filter(el => Number(el.dataset.cardId) !== d.id)
    let index = cards.length
    for (let i = 0; i < cards.length; i++) { const r = cards[i].getBoundingClientRect(); if (p.y < r.top + r.height / 2) { index = i; break } }
    if (over !== d.over || index !== d.index) setDrag({ ...d, over, index })
  }
  const autoScroll = () => {
    const p = ptr.current
    if (!p || !dragRef.current || !scroller.current) { raf.current = null; return }
    const r = scroller.current.getBoundingClientRect()
    const edge = 60, speed = 14
    let moved = false
    if (p.x < r.left + edge) { scroller.current.scrollLeft -= speed * (1 - Math.max(0, p.x - r.left) / edge); moved = true }
    else if (p.x > r.right - edge) { scroller.current.scrollLeft += speed * (1 - Math.max(0, r.right - p.x) / edge); moved = true }
    if (dragRef.current.kind === 'card') {
      for (const body of scroller.current.querySelectorAll<HTMLElement>('.lc-cards')) {
        const b = body.getBoundingClientRect()
        if (p.x < b.left || p.x > b.right) continue
        if (p.y < b.top + 40 && p.y > b.top - 30) { body.scrollTop -= 10; moved = true }
        else if (p.y > b.bottom - 40 && p.y < b.bottom + 30) { body.scrollTop += 10; moved = true }
      }
    }
    if (moved) computeTarget()
    raf.current = requestAnimationFrame(autoScroll)
  }
  const start = () => {
    const p = ptr.current
    if (!p || !view) return
    p.started = true
    const r = p.el.getBoundingClientRect()
    p.offX = p.sx - r.left; p.offY = p.sy - r.top
    if (p.touch) navigator.vibrate?.(8)
    if (p.kind === 'card') {
      const card = view.lists.flatMap(l => l.cards || []).find(c => c.id === p.id)
      if (!card) return
      const col = scroller.current?.querySelector<HTMLElement>(`[data-list-col="${card.list_id}"]`)
      const siblings = col ? [...col.querySelectorAll<HTMLElement>('[data-card-id]')] : []
      const index = Math.max(0, siblings.findIndex(el => Number(el.dataset.cardId) === p.id))
      setDrag({ kind: 'card', id: p.id, from: card.list_id, over: card.list_id, index, w: r.width, h: r.height })
    } else {
      const index = view.lists.findIndex(l => l.id === p.id)
      setDrag({ kind: 'list', id: p.id, index, w: r.width, h: Math.min(r.height, 420) })
    }
    requestAnimationFrame(positionGhost)
    if (!raf.current) raf.current = requestAnimationFrame(autoScroll)
  }
  const onPointerDown = (e: React.PointerEvent, kind: 'card' | 'list', itemId: number) => {
    if (!canEdit || e.button > 0 || ptr.current) return
    if ((e.target as HTMLElement).closest('button, input, textarea, a, [data-no-drag]') && kind === 'list') return
    const touch = e.pointerType !== 'mouse'
    ptr.current = { x: e.clientX, y: e.clientY, sx: e.clientX, sy: e.clientY, offX: 0, offY: 0, started: false, timer: null, kind, id: itemId, el: e.currentTarget as HTMLElement, pid: e.pointerId, touch }
    if (touch) ptr.current.timer = window.setTimeout(() => { if (ptr.current && !ptr.current.started) start() }, 320)
  }
  useEffect(() => {
    const move = (e: PointerEvent) => {
      const p = ptr.current
      if (!p || e.pointerId !== p.pid) return
      p.x = e.clientX; p.y = e.clientY
      if (!p.started) {
        const dist = Math.hypot(p.x - p.sx, p.y - p.sy)
        if (p.touch) { if (dist > 8) { if (p.timer) clearTimeout(p.timer); ptr.current = null } return }
        if (dist < 5) return
        start()
      }
      positionGhost()
      computeTarget()
    }
    const end = (e: PointerEvent) => {
      const p = ptr.current
      if (!p || e.pointerId !== p.pid) return
      if (p.timer) clearTimeout(p.timer)
      ptr.current = null
      if (raf.current) { cancelAnimationFrame(raf.current); raf.current = null }
      const d = dragRef.current
      if (!p.started || !d) return
      suppressClick.current = Date.now()
      if (e.type === 'pointercancel') { setDrag(null); return }
      drop(d)
    }
    const block = (e: TouchEvent) => { if (dragRef.current) e.preventDefault() }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
    document.addEventListener('touchmove', block, { passive: false })
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', end); window.removeEventListener('pointercancel', end); document.removeEventListener('touchmove', block) }
  })

  const drop = async (d: Drag) => {
    if (!view) { setDrag(null); return }
    if (d.kind === 'list') {
      const from = view.lists.findIndex(l => l.id === d.id)
      const lists = view.lists.filter(l => l.id !== d.id)
      lists.splice(d.index, 0, view.lists[from])
      setView({ ...view, lists })
      setDrag(null)
      if (d.index !== from) { try { await api.put(`${V1}/lists/${d.id}`, { position: d.index }) } catch (e) { toast((e as Error).message, 'err') } }
      after()
      return
    }
    const target = view.lists.find(l => l.id === d.over)
    const card = view.lists.flatMap(l => l.cards || []).find(c => c.id === d.id)
    if (!target || !card) { setDrag(null); return }
    const full = (target.cards || []).filter(c => c.id !== d.id)
    const shown = full.filter(visible)
    let abs = full.length
    if (d.index < shown.length) abs = full.indexOf(shown[d.index])
    else if (shown.length) abs = full.indexOf(shown[shown.length - 1]) + 1
    const oldIndex = (view.lists.find(l => l.id === d.from)?.cards || []).findIndex(c => c.id === d.id)
    const same = d.from === d.over && oldIndex === abs
    const fromList = view.lists.find(l => l.id === d.from)
    const moved: Card = { ...card, list_id: d.over, done: target.is_done ? true : fromList?.is_done && !target.is_done ? false : card.done }
    const lists = view.lists.map(l => {
      let cards = (l.cards || []).filter(c => c.id !== d.id)
      if (l.id === d.over) { cards = [...cards]; cards.splice(abs, 0, moved) }
      return { ...l, cards }
    })
    setView({ ...view, lists })
    setDrag(null)
    if (!same) { try { await api.put(`${V1}/cards/${d.id}`, { list_id: d.over, position: abs }) } catch (e) { toast((e as Error).message, 'err') } }
    after()
  }
  const after = () => { pending.current = false; load(); announceChange() }

  // ---------------- actions ----------------
  const patchBoard = async (patch: Partial<Board>, msg?: string) => {
    if (!view) return
    setView({ ...view, board: { ...view.board, ...patch } })
    try { await api.put(`${V1}/boards/${id}`, patch); if (msg) toast(msg); changed() } catch (e) { toast((e as Error).message, 'err'); load() }
  }
  const openCard = (c: Card) => { if (Date.now() - suppressClick.current < 300) return; navigate(`/b/${id}/c/${c.id}`) }

  if (err) return <div className="board-msg"><Empty icon="grid" title="Can’t open this board">{err}</Empty></div>
  if (!view || !board) return <div className="board-msg"><Loading /></div>

  const lists = view.lists
  const dragList = drag?.kind === 'list' ? lists.find(l => l.id === drag.id) : null
  const dragCard = drag?.kind === 'card' ? lists.flatMap(l => l.cards || []).find(c => c.id === drag.id) : null
  const shownLists = drag?.kind === 'list' ? lists.filter(l => l.id !== drag.id) : lists
  const density = settings?.card_density === 'compact'

  return (
    <div className={`board ${density ? 'compact' : ''} ${drag ? 'is-dragging' : ''}`} style={{ '--board': hex(board.color) } as React.CSSProperties}>
      <BoardHeader view={view} canEdit={canEdit} filterOpen={filterOpen} filterCount={filterCount} panel={panel}
        onPatch={patchBoard} onFilter={() => setFilterOpen(o => !o)} onPanel={p => setPanel(cur => cur === p ? '' : p)} onDialog={setDialog} />
      {filterOpen && <FilterBar filter={filter} setFilter={setFilter} labels={labels} members={members} inputRef={filterInput} onClose={() => { setFilterOpen(false); setFilter(emptyFilter(settings?.show_done_cards === false)) }} />}
      <div className="board-body">
        <div className={`lists ${drag ? 'dragging' : ''}`} ref={scroller}>
          {shownLists.map((l, i) => (
            <React.Fragment key={l.id}>
              {drag?.kind === 'list' && drag.index === i && <div className="list-col placeholder" style={{ height: drag.h }} />}
              <ListColumn list={l} labels={labels} members={members} canEdit={canEdit} visible={visible} drag={drag} composer={composer === l.id}
                onComposer={open => setComposer(open ? l.id : null)} onOpen={openCard} onPointerDown={onPointerDown} onChanged={changed} />
            </React.Fragment>
          ))}
          {drag?.kind === 'list' && drag.index >= shownLists.length && <div className="list-col placeholder" style={{ height: drag.h }} />}
          {canEdit && <AddList boardId={id} onAdded={changed} />}
          {lists.length === 0 && !canEdit && <p className="muted">This board has no lists yet.</p>}
        </div>
        {panel === 'activity' && <ActivityPanel boardId={id} onClose={() => setPanel('')} onOpenCard={cid => navigate(`/b/${id}/c/${cid}`)} />}
      </div>

      {drag && createPortal(
        <div className={`drag-ghost ${drag.kind}`} ref={ghost} style={{ width: drag.w }}>
          {dragCard && <div className="cardface"><CardFace card={dragCard} labels={labels} members={members} compact={density} dense={density} /></div>}
          {dragList && <div className="list-col ghost-list"><div className="lc-head"><span className="lc-title">{dragList.title}</span><span className="lc-count">{(dragList.cards || []).length}</span></div></div>}
        </div>, document.body)}

      {cardId && <CardModal cardId={Number(cardId)} boardView={view} onClose={() => navigate(`/b/${id}`)} onChanged={changed} />}
      {dialog === 'share' && <ShareModal view={view} me={user?.id || 0} onClose={() => setDialog('')} onChanged={changed} />}
      {dialog === 'archived' && <ArchivedModal boardId={id} labels={labels} members={members} canEdit={canEdit} onClose={() => setDialog('')} onChanged={changed} />}
      {dialog === 'about' && <AboutModal board={board} canEdit={canEdit} onClose={() => setDialog('')} onSave={d => { setDialog(''); patchBoard({ description: d }, 'Saved') }} />}
      {dialog === 'copy' && <CopyModal board={board} onClose={() => setDialog('')} onCopied={b => { setDialog(''); announceChange(); navigate(`/b/${b.id}`) }} />}
      {dialog === 'delete' && <Confirm title={`Delete “${board.title}”?`} confirmLabel="Move to the bin" message="The board goes to the bin with everything on it. You can restore it from the bin for 30 days."
        onCancel={() => setDialog('')} onConfirm={async () => { setDialog(''); try { await api.del(`${V1}/boards/${id}`); announceChange(); navigate('/'); toast('Board moved to the bin', 'ok', { label: 'Undo', run: async () => { await api.post(`${V1}/boards/${id}/restore`); announceChange(); navigate(`/b/${id}`) } }) } catch (e) { toast((e as Error).message, 'err') } }} />}
      {dialog === 'leave' && <Confirm title={`Leave “${board.title}”?`} confirmLabel="Leave" message="You will no longer see this board. The owner can share it with you again."
        onCancel={() => setDialog('')} onConfirm={async () => { setDialog(''); try { await api.del(`${V1}/boards/${id}/members/${user?.id}`); announceChange(); navigate('/') } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

// ---------------- header ----------------

function BoardHeader({ view, canEdit, filterOpen, filterCount, panel, onPatch, onFilter, onPanel, onDialog }: {
  view: BoardView; canEdit: boolean; filterOpen: boolean; filterCount: number; panel: string
  onPatch: (p: Partial<Board>, msg?: string) => void; onFilter: () => void; onPanel: (p: 'activity') => void; onDialog: (d: 'share' | 'archived' | 'about' | 'copy' | 'delete' | 'leave') => void
}) {
  const b = view.board
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(b.title)
  const [look, setLook] = useState<Anchor | null>(null)
  const [menu, setMenu] = useState<Anchor | null>(null)
  useEffect(() => setTitle(b.title), [b.title])
  const save = () => { setEditing(false); const t = title.trim(); if (t && t !== b.title) onPatch({ title: t }); else setTitle(b.title) }
  const items: MenuItem[] = [
    { label: 'About this board', icon: 'info', onSelect: () => onDialog('about') },
    { label: 'Archived cards', icon: 'archive', onSelect: () => onDialog('archived') },
    { label: 'Activity', icon: 'activity', onSelect: () => onPanel('activity') },
    'sep',
    { label: 'Copy board', icon: 'copy', onSelect: () => onDialog('copy') },
    ...(canEdit ? [{ label: b.archived ? 'Unarchive board' : 'Archive board', icon: 'archive', onSelect: () => onPatch({ archived: !b.archived }, b.archived ? 'Board is back' : 'Board archived') }] : []),
    ...(b.role === 'owner' ? ['sep' as const, { label: 'Delete board', icon: 'trash', danger: true, onSelect: () => onDialog('delete') }] : ['sep' as const, { label: 'Leave board', icon: 'log-out', danger: true, onSelect: () => onDialog('leave') }]),
  ]
  return (
    <div className="board-head">
      <button type="button" className="bh-emoji" style={{ background: hex(b.color), color: inkOn(b.color) }} disabled={!canEdit} onClick={e => setLook(rectOf(e.currentTarget))} aria-label="Colour and emoji" title={canEdit ? 'Colour and emoji' : undefined}>
        {b.emoji || <Icon name="grid" size={16} />}
      </button>
      {editing ? <input className="bh-title-in" value={title} autoFocus onChange={e => setTitle(e.target.value)} onBlur={save} onKeyDown={e => { if (e.key === 'Enter') save(); if (e.key === 'Escape') { setTitle(b.title); setEditing(false) } }} maxLength={120} />
        : <h1 className={`bh-title ${canEdit ? 'editable' : ''}`} onClick={() => canEdit && setEditing(true)} title={canEdit ? 'Rename' : undefined}>{b.title}</h1>}
      <button type="button" className={`btn ghost icon-only bh-star ${b.starred ? 'on' : ''}`} aria-label={b.starred ? 'Unstar' : 'Star'} title={b.starred ? 'Unstar' : 'Star'} onClick={() => onPatch({ starred: !b.starred })}><Icon name="star" size={17} /></button>
      {b.archived && <span className="badge warn">archived</span>}
      {b.role === 'viewer' && <span className="badge">view only</span>}
      <span className="spacer" />
      <button type="button" className="bh-people" onClick={() => onDialog('share')} title="People on this board">
        <Avatars ids={view.members.map(m => m.user_id)} members={view.members} size={26} max={4} />
        {b.role === 'owner' && <span className="bh-share"><Icon name="users" size={15} /><span className="hide-sm">Share</span></span>}
      </button>
      <button type="button" className={`btn ${filterOpen || filterCount ? 'active-soft' : 'ghost'}`} onClick={onFilter} title="Filter (/ or f)"><Icon name="filter" size={15} /><span className="hide-sm">Filter</span>{filterCount > 0 && <span className="badge accent">{filterCount}</span>}</button>
      <button type="button" className={`btn icon-only ${panel === 'activity' ? 'active-soft' : 'ghost'} hide-sm`} onClick={() => onPanel('activity')} aria-label="Activity" title="Activity"><Icon name="activity" size={16} /></button>
      <button type="button" className="btn ghost icon-only" aria-label="Board menu" title="Board menu" onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}><Icon name="more" size={18} /></button>
      {menu && <Menu anchor={menu} align="right" items={items} onClose={() => setMenu(null)} width={220} />}
      {look && <Popover anchor={look} onClose={() => setLook(null)} prefer="below" width={300} label="Colour and emoji">
        <div className="pop-pad stack tight">
          <div className="pop-h">Colour</div>
          <Swatches value={b.color} onChange={c => onPatch({ color: c })} />
          <div className="pop-h">Emoji</div>
          <div className="emoji-row">{EMOJIS.map(e => <button key={e || 'none'} type="button" className={`emoji-btn ${b.emoji === e ? 'on' : ''}`} onClick={() => onPatch({ emoji: e })} aria-label={e || 'No emoji'}>{e || <Icon name="x" size={12} />}</button>)}</div>
        </div>
      </Popover>}
    </div>
  )
}

// ---------------- filter ----------------

function FilterBar({ filter, setFilter, labels, members, inputRef, onClose }: { filter: Filter; setFilter: (f: Filter) => void; labels: Label[]; members: Member[]; inputRef: React.RefObject<HTMLInputElement | null>; onClose: () => void }) {
  const toggle = (k: 'labels' | 'members', v: number) => setFilter({ ...filter, [k]: filter[k].includes(v) ? filter[k].filter(x => x !== v) : [...filter[k], v] })
  return (
    <div className="filter-bar">
      <div className="search-box fb-search"><Icon name="search" size={15} /><input ref={inputRef} value={filter.q} onChange={e => setFilter({ ...filter, q: e.target.value })} placeholder="Filter cards…" aria-label="Filter cards" onKeyDown={e => { if (e.key === 'Escape') (e.target as HTMLInputElement).blur() }} /></div>
      <div className="fb-group">
        {labels.map(l => <button key={l.id} type="button" className={`fb-chip ${filter.labels.includes(l.id) ? 'on' : ''}`} onClick={() => toggle('labels', l.id)}><span className="fb-dot" style={{ background: hex(l.color) }} />{l.title || 'Label'}</button>)}
      </div>
      <div className="fb-group">
        {members.map(m => <button key={m.user_id} type="button" className={`fb-chip ${filter.members.includes(m.user_id) ? 'on' : ''}`} onClick={() => toggle('members', m.user_id)} title={m.email}><Avatar name={m.name} email={m.email} size={16} />{(m.name || m.email).split(' ')[0]}</button>)}
        <button type="button" className={`fb-chip ${filter.members.includes(-1) ? 'on' : ''}`} onClick={() => toggle('members', -1)}>Nobody</button>
      </div>
      <Segmented size="sm" value={filter.due} onChange={v => setFilter({ ...filter, due: filter.due === v ? '' : v })} options={[{ value: 'overdue', label: 'Overdue' }, { value: 'today', label: 'Today' }, { value: 'week', label: 'This week' }, { value: 'none', label: 'No date' }]} />
      <label className="check-row"><input type="checkbox" checked={filter.hideDone} onChange={e => setFilter({ ...filter, hideDone: e.target.checked })} />Hide done</label>
      <span className="spacer" />
      <button type="button" className="btn ghost sm" onClick={onClose}>Clear</button>
    </div>
  )
}

// ---------------- a list ----------------

function ListColumn({ list, labels, members, canEdit, visible, drag, composer, onComposer, onOpen, onPointerDown, onChanged }: {
  list: List; labels: Label[]; members: Member[]; canEdit: boolean; visible: (c: Card) => boolean; drag: Drag | null; composer: boolean
  onComposer: (open: boolean) => void; onOpen: (c: Card) => void; onPointerDown: (e: React.PointerEvent, kind: 'card' | 'list', id: number) => void; onChanged: () => void
}) {
  const { toast } = useToast()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(list.title)
  const [dialog, setDialog] = useState<'' | 'wip' | 'archive' | 'delete'>('')
  useEffect(() => setTitle(list.title), [list.title])
  const all = list.cards || []
  const cards = all.filter(visible)
  const shown = drag?.kind === 'card' ? cards.filter(c => c.id !== drag.id) : cards
  const placeholderAt = drag?.kind === 'card' && drag.over === list.id ? drag.index : -1
  const open = all.filter(c => !c.done).length
  const over = list.wip_limit > 0 && open > list.wip_limit
  const put = async (patch: Partial<List>, msg?: string) => { try { await api.put(`${V1}/lists/${list.id}`, patch); if (msg) toast(msg); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  const saveTitle = () => { setEditing(false); const t = title.trim(); if (t && t !== list.title) put({ title: t }); else setTitle(list.title) }
  const sort = async (by: string) => { try { await api.post(`${V1}/lists/${list.id}/sort`, { by }); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  const items: MenuItem[] = [
    { label: 'Add a card', icon: 'plus', onSelect: () => onComposer(true) },
    { label: 'Rename', icon: 'edit', onSelect: () => setEditing(true) },
    { label: list.wip_limit ? `Card limit: ${list.wip_limit}` : 'Set a card limit', icon: 'gauge', onSelect: () => setDialog('wip') },
    { label: 'Done list', icon: 'check-circle', checked: list.is_done, onSelect: () => put({ is_done: !list.is_done }, list.is_done ? 'No longer a done list' : 'Cards moved here are marked done') },
    'sep',
    { heading: 'Sort cards' },
    { label: 'By due date', icon: 'clock', onSelect: () => sort('due') },
    { label: 'By name', icon: 'text', onSelect: () => sort('title') },
    { label: 'Newest first', icon: 'calendar', onSelect: () => sort('created') },
    'sep',
    { label: 'Archive every card', icon: 'archive', disabled: !all.length, onSelect: () => setDialog('archive') },
    { label: 'Delete list', icon: 'trash', danger: true, onSelect: () => setDialog('delete') },
  ]
  return (
    <div className={`list-col ${list.is_done ? 'is-done' : ''}`} data-list-col={list.id}>
      <div className={`lc-head ${canEdit ? 'grab' : ''}`} onPointerDown={e => onPointerDown(e, 'list', list.id)}>
        {editing ? <input className="lc-title-in" value={title} autoFocus onChange={e => setTitle(e.target.value)} onBlur={saveTitle} onKeyDown={e => { if (e.key === 'Enter') saveTitle(); if (e.key === 'Escape') { setTitle(list.title); setEditing(false) } }} maxLength={120} data-no-drag />
          : <span className="lc-title" onDoubleClick={() => canEdit && setEditing(true)}>{list.title}</span>}
        {list.is_done && <Icon name="check-circle" size={14} className="lc-done-ico" />}
        <span className={`lc-count ${over ? 'over' : ''}`} title={list.wip_limit ? `${open} open of a limit of ${list.wip_limit}` : `${all.length} cards`}>{list.wip_limit ? `${open}/${list.wip_limit}` : all.length}</span>
        {canEdit && <button type="button" className="btn ghost icon-only sm" aria-label="List menu" title="List menu" onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}><Icon name="more" size={16} /></button>}
        {menu && <Menu anchor={menu} align="right" items={items} onClose={() => setMenu(null)} width={220} />}
      </div>
      <div className="lc-cards">
        {shown.map((c, i) => (
          <React.Fragment key={c.id}>
            {placeholderAt === i && <div className="card-ph" style={{ height: drag!.h }} />}
            <div className={`cardface ${canEdit ? 'grab' : ''} ${c.done ? 'is-done' : ''}`} data-card-id={c.id} role="button" tabIndex={0}
              onPointerDown={e => onPointerDown(e, 'card', c.id)} onContextMenu={e => { if (canEdit && (e.nativeEvent as PointerEvent).pointerType !== 'mouse') e.preventDefault() }} onClick={() => onOpen(c)} onKeyDown={e => { if (e.key === 'Enter') onOpen(c) }}>
              <CardFace card={c} labels={labels} members={members} compact={false} dense={false} />
            </div>
          </React.Fragment>
        ))}
        {placeholderAt >= shown.length && <div className="card-ph" style={{ height: drag!.h }} />}
        {all.length > 0 && cards.length === 0 && placeholderAt < 0 && <p className="lc-hidden">{all.length} hidden by the filter</p>}
      </div>
      {canEdit && (composer ? <Composer listId={list.id} onClose={() => onComposer(false)} onAdded={onChanged} labels={labels} members={members} />
        : <button type="button" className="lc-add" onClick={() => onComposer(true)}><Icon name="plus" size={15} />Add a card</button>)}
      {dialog === 'wip' && <WipModal list={list} onClose={() => setDialog('')} onSave={n => { setDialog(''); put({ wip_limit: n }, n ? `At most ${n} open cards` : 'No card limit') }} />}
      {dialog === 'archive' && <Confirm title={`Archive every card in “${list.title}”?`} confirmLabel="Archive" danger={false} message="They leave the board but stay under Archived cards, where you can bring them back."
        onCancel={() => setDialog('')} onConfirm={async () => { setDialog(''); try { const r = await api.post<{ archived: number }>(`${V1}/lists/${list.id}/archive-cards`); toast(`${r.archived} cards archived`); onChanged() } catch (e) { toast((e as Error).message, 'err') } }} />}
      {dialog === 'delete' && <Confirm title={`Delete “${list.title}”?`} confirmLabel="Move to the bin" message={all.length ? `The list and its ${all.length} cards go to the bin.` : 'The list goes to the bin.'}
        onCancel={() => setDialog('')} onConfirm={async () => { setDialog(''); try { await api.del(`${V1}/lists/${list.id}`); onChanged(); toast('List deleted') } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

function Composer({ listId, onClose, onAdded, labels, members }: { listId: number; onClose: () => void; onAdded: () => void; labels: Label[]; members: Member[] }) {
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => { ref.current?.focus(); ref.current?.scrollIntoView({ block: 'nearest' }) }, [])
  const add = async () => {
    const t = text.trim()
    if (!t) return
    setBusy(true)
    try { await api.post(`${V1}/lists/${listId}/cards`, { title: t, quick: true, position: -1 }); setText(''); onAdded(); ref.current?.focus() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  // suggest what # and @ can be followed by
  const m = /([#@])([^\s#@!]*)$/.exec(text)
  const sugg = m ? (m[1] === '#' ? labels.filter(l => l.title && l.title.toLowerCase().replace(/\s+/g, '-').startsWith(m[2].toLowerCase())).map(l => ({ k: `l${l.id}`, label: l.title, ins: l.title.replace(/\s+/g, '-') }))
    : members.filter(p => (p.name || p.email).toLowerCase().startsWith(m[2].toLowerCase())).map(p => ({ k: `m${p.user_id}`, label: p.name || p.email, ins: (p.name || p.email.split('@')[0]).split(' ')[0].toLowerCase() }))).slice(0, 5) : []
  const pick = (ins: string) => { setText(text.replace(/([#@])([^\s#@!]*)$/, `$1${ins} `)); ref.current?.focus() }
  return (
    <div className="composer">
      <textarea ref={ref} rows={2} value={text} onChange={e => setText(e.target.value)} placeholder="What needs doing?" aria-label="New card"
        onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (sugg.length && m && m[2]) pick(sugg[0].ins); else add() } if (e.key === 'Escape') { e.stopPropagation(); onClose() } }} />
      {sugg.length > 0 && <div className="composer-sugg">{sugg.map(s => <button key={s.k} type="button" onMouseDown={e => { e.preventDefault(); pick(s.ins) }}>{m![1]}{s.label}</button>)}</div>}
      <div className="composer-hint"><b>#</b>label <b>@</b>person <b>!</b>due (today, fri, 3d, 17:00)</div>
      <div className="row"><button type="button" className="btn primary sm" onClick={add} disabled={busy || !text.trim()}>Add card</button><button type="button" className="btn ghost sm" onClick={onClose}>Done</button></div>
    </div>
  )
}

function AddList({ boardId, onAdded }: { boardId: number; onAdded: () => void }) {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [title, setTitle] = useState('')
  const add = async () => {
    const t = title.trim()
    if (!t) return
    try { await api.post(`${V1}/boards/${boardId}/lists`, { title: t, position: -1 }); setTitle(''); onAdded() } catch (e) { toast((e as Error).message, 'err') }
  }
  if (!open) return <button type="button" className="add-list" onClick={() => setOpen(true)}><Icon name="plus" size={15} />Add a list</button>
  return (
    <div className="list-col add-list-form">
      <input autoFocus value={title} onChange={e => setTitle(e.target.value)} placeholder="List name" onKeyDown={e => { if (e.key === 'Enter') add(); if (e.key === 'Escape') setOpen(false) }} maxLength={120} />
      <div className="row"><button type="button" className="btn primary sm" onClick={add} disabled={!title.trim()}>Add list</button><button type="button" className="btn ghost sm" onClick={() => setOpen(false)}>Done</button></div>
    </div>
  )
}

function WipModal({ list, onClose, onSave }: { list: List; onClose: () => void; onSave: (n: number) => void }) {
  const [n, setN] = useState(String(list.wip_limit || ''))
  return (
    <Modal title="Card limit" onClose={onClose} size="sm" footer={<><button type="button" className="btn" onClick={() => onSave(0)}>No limit</button><button type="button" className="btn primary" onClick={() => onSave(Math.max(0, Number(n) || 0))}>Save</button></>}>
      <Field label={`At most this many open cards in “${list.title}”`} help="The count turns amber when there are more — a nudge to finish things before starting new ones.">
        <input type="number" min={0} max={999} value={n} onChange={e => setN(e.target.value)} autoFocus onKeyDown={e => { if (e.key === 'Enter') onSave(Math.max(0, Number(n) || 0)) }} />
      </Field>
    </Modal>
  )
}

// ---------------- side panel: activity ----------------

function ActivityPanel({ boardId, onClose, onOpenCard }: { boardId: number; onClose: () => void; onOpenCard: (id: number) => void }) {
  const [items, setItems] = useState<Activity[] | null>(null)
  const [more, setMore] = useState(true)
  const load = useCallback(async (before = 0) => {
    const r = await api.get<Activity[]>(`${V1}/boards/${boardId}/activity?limit=40${before ? `&before=${before}` : ''}`)
    setItems(cur => before && cur ? [...cur, ...r] : r)
    setMore(r.length === 40)
  }, [boardId])
  useEffect(() => { load().catch(() => setItems([])) }, [load])
  return (
    <aside className="side-panel" aria-label="Activity">
      <div className="sp-head"><h3>Activity</h3><button type="button" className="btn ghost icon-only sm" onClick={onClose} aria-label="Close"><Icon name="x" size={16} /></button></div>
      <div className="sp-body">
        {!items ? <Loading /> : items.length === 0 ? <p className="muted small">Nothing yet.</p> : items.map(a => (
          <div key={a.id} className="act-row">
            <Avatar name={a.user_name} size={22} />
            <div className="grow">
              <div className="act-text"><b>{a.user_name || 'Someone'}</b> {a.card_id ? <button type="button" className="link-btn" onClick={() => onOpenCard(a.card_id!)}>{describeActivity(a.kind, a.detail)}</button> : describeActivity(a.kind, a.detail)}</div>
              <div className="faint xsmall">{ago(a.created_at)}{a.via ? ` · via ${a.via === 'deck' ? 'a Deck app' : a.via}` : ''}</div>
            </div>
          </div>
        ))}
        {items && more && <button type="button" className="btn ghost sm block" onClick={() => load(items[items.length - 1]?.id || 0)}>Older</button>}
      </div>
    </aside>
  )
}

// ---------------- dialogs ----------------

function ShareModal({ view, me, onClose, onChanged }: { view: BoardView; me: number; onClose: () => void; onChanged: () => void }) {
  const { toast } = useToast()
  const owner = view.board.role === 'owner'
  const [members, setMembers] = useState<Member[]>(view.members)
  const people = useAsync(() => owner ? api.get<Person[]>(`${V1}/people`) : Promise.resolve([] as Person[]), [owner])
  const [pick, setPick] = useState('')
  const [role, setRole] = useState<Role>('editor')
  const ids = new Set(members.map(m => m.user_id))
  const options = (people.data || []).filter(p => !ids.has(p.id)).map(p => ({ value: String(p.id), label: p.name || p.email, hint: p.name ? p.email : undefined }))
  const run = async (fn: () => Promise<Member[] | unknown>, msg: string) => {
    try { const r = await fn(); if (Array.isArray(r)) setMembers(r as Member[]); else { const v = await api.get<Member[]>(`${V1}/boards/${view.board.id}/members`); setMembers(v) } toast(msg); onChanged() } catch (e) { toast((e as Error).message, 'err') }
  }
  return (
    <Modal title="People on this board" onClose={onClose}>
      <div className="stack">
        {owner && <div className="share-add">
          <Select value={pick} onChange={setPick} options={options} placeholder={people.loading ? 'Loading…' : options.length ? 'Choose someone on this box…' : 'Everyone already has access'} ariaLabel="Person" disabled={!options.length} />
          <Segmented size="sm" value={role} onChange={setRole} options={[{ value: 'editor', label: 'Can edit' }, { value: 'viewer', label: 'Can view' }]} />
          <button type="button" className="btn primary" disabled={!pick} onClick={() => { const uid = Number(pick); setPick(''); run(() => api.post(`${V1}/boards/${view.board.id}/members`, { user_id: uid, role }), 'Shared') }}>Share</button>
        </div>}
        <div className="share-list">
          {members.map(m => (
            <div key={m.user_id} className="share-row">
              <Avatar name={m.name} email={m.email} size={30} />
              <div className="grow"><div>{m.name || m.email}{m.user_id === me && <span className="badge">you</span>}</div><div className="muted small ellipsis">{m.email}</div></div>
              {m.role === 'owner' ? <span className="badge accent">owner</span>
                : owner ? <>
                  <Segmented size="sm" value={m.role} onChange={r => run(() => api.put(`${V1}/boards/${view.board.id}/members/${m.user_id}`, { role: r }), 'Saved')} options={[{ value: 'editor', label: 'Edit' }, { value: 'viewer', label: 'View' }]} />
                  <button type="button" className="btn ghost icon-only sm" aria-label="Remove" title="Remove" onClick={() => run(() => api.del(`${V1}/boards/${view.board.id}/members/${m.user_id}`), 'Removed')}><Icon name="x" size={15} /></button>
                </> : <span className="badge">{m.role === 'editor' ? 'can edit' : 'can view'}</span>}
            </div>
          ))}
        </div>
        <p className="muted small">{owner ? 'People you share with see this board on their home page and can be assigned to cards. Editors change everything except who it is shared with; viewers only look.' : 'Only the owner can change who this board is shared with.'}</p>
      </div>
    </Modal>
  )
}

function ArchivedModal({ boardId, labels, members, canEdit, onClose, onChanged }: { boardId: number; labels: Label[]; members: Member[]; canEdit: boolean; onClose: () => void; onChanged: () => void }) {
  const { toast } = useToast()
  const { data, loading, reload } = useAsync(() => api.get<BoardView>(`${V1}/boards/${boardId}?archived=1`), [boardId])
  const cards = useMemo(() => (data?.lists || []).flatMap(l => (l.cards || []).map(c => ({ ...c, list_title: l.title }))), [data])
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); reload(); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title="Archived cards" onClose={onClose} size="lg">
      {loading ? <Loading /> : cards.length === 0 ? <Empty icon="archive" title="No archived cards" /> : (
        <div className="card-list">
          {cards.map(c => (
            <div key={c.id} className="arch-card">
              <div className="cardface grow"><CardFace card={c} labels={labels} members={members} /><div className="faint xsmall arch-where">in {c.list_title}</div></div>
              {canEdit && <div className="row">
                <button type="button" className="btn sm" onClick={() => run(() => api.put(`${V1}/cards/${c.id}`, { archived: false }), 'Back on the board')}>Unarchive</button>
                <button type="button" className="btn ghost sm danger-text" onClick={() => run(() => api.del(`${V1}/cards/${c.id}`), 'Moved to the bin')}>Delete</button>
              </div>}
            </div>
          ))}
        </div>
      )}
    </Modal>
  )
}

function AboutModal({ board, canEdit, onClose, onSave }: { board: Board; canEdit: boolean; onClose: () => void; onSave: (d: string) => void }) {
  const [d, setD] = useState(board.description)
  return (
    <Modal title={`About “${board.title}”`} onClose={onClose} footer={canEdit ? <><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={() => onSave(d)}>Save</button></> : undefined}>
      <div className="stack">
        {canEdit ? <Field label="Description" help="What the board is for, who does what — Markdown works."><textarea rows={6} value={d} onChange={e => setD(e.target.value)} /></Field>
          : <p>{board.description || <span className="muted">No description.</span>}</p>}
        <dl className="set-dl">
          <div><dt>Made</dt><dd>{ago(board.created_at)}</dd></div>
          <div><dt>Changed</dt><dd>{ago(board.updated_at)}</dd></div>
          {board.source && board.source !== 'app' && <div><dt>Came from</dt><dd>{board.source === 'deck' ? 'Nextcloud Deck' : board.source === 'trello' ? 'Trello' : board.source}</dd></div>}
          <div><dt>Your role</dt><dd>{board.role}</dd></div>
        </dl>
      </div>
    </Modal>
  )
}

function CopyModal({ board, onClose, onCopied }: { board: Board; onClose: () => void; onCopied: (b: Board) => void }) {
  const { toast } = useToast()
  const [title, setTitle] = useState(`${board.title} (copy)`)
  const [cards, setCards] = useState(true)
  const [busy, setBusy] = useState(false)
  const copy = async () => { setBusy(true); try { onCopied(await api.post<Board>(`${V1}/boards/${board.id}/copy`, { title: title.trim() || board.title, cards })) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) } }
  return (
    <Modal title="Copy board" onClose={onClose} size="sm" footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={copy} disabled={busy}>Copy</button></>}>
      <div className="stack">
        <Field label="Name of the copy"><input value={title} onChange={e => setTitle(e.target.value)} autoFocus /></Field>
        <label className="check-row"><input type="checkbox" checked={cards} onChange={e => setCards(e.target.checked)} />Copy the cards too (lists and labels are always copied)</label>
        <p className="muted small">The copy is yours and not shared with anyone.</p>
      </div>
    </Modal>
  )
}

