// ⌘K — jump to a board or a card, or run a command from the keyboard.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { hex } from '../lib/colors'
import { fmtDue } from '../lib/format'
import type { Board, Card, SearchResult } from '../lib/types'
import { Icon } from './icons'
import { useDebounce } from './ui'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

export function CommandPalette({ onClose, commands, boards }: { onClose: () => void; commands: PaletteCommand[]; boards: Board[] }) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [cards, setCards] = useState<Card[]>([])
  const list = useRef<HTMLDivElement>(null)
  const dq = useDebounce(q.trim(), 200)
  useEffect(() => {
    if (dq.length < 2) { setCards([]); return }
    let live = true
    api.get<SearchResult>(`${V1}/search?q=${encodeURIComponent(dq)}`).then(r => { if (live) setCards((r.cards || []).slice(0, 12)) }).catch(() => {})
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const bs = boards.filter(b => !n || hit(b.title)).slice(0, n ? 8 : 6)
      .map(b => ({ key: `b${b.id}`, group: 'Boards', icon: <span className="pal-board" style={{ background: hex(b.color) }}>{b.emoji}</span>, label: b.title, sub: b.archived ? 'archived' : undefined, run: () => navigate(`/b/${b.id}`) }))
    const cs = (n ? cards : []).map(c => ({ key: `c${c.id}`, group: 'Cards', icon: <Icon name={c.done ? 'check-circle' : 'circle'} size={16} />, label: c.title,
      sub: <>{c.board_title} · {c.list_title}{c.due_at ? ` · ${fmtDue(c.due_at)}` : ''}</>, run: () => navigate(`/b/${c.board_id}/c/${c.id}`) }))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.keywords || ''}`)).map(c => ({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run }))
    const search = n.length >= 2 ? [{ key: 'search', group: 'Search', icon: <Icon name="search" size={16} />, label: <>Search everywhere for “{q.trim()}”</>, run: () => navigate(`/search?q=${encodeURIComponent(q.trim())}`) }] : []
    return [...bs, ...cs, ...cmds, ...search]
  }, [q, boards, cards, commands, navigate])
  useEffect(() => setHi(0), [q])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Find a board or a card, or run a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">Nothing matches.</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label">{r.label}</span>{r.sub && <span className="pal-sub">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
