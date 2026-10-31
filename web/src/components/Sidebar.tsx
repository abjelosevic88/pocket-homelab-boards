// Left column: New, the sections, starred boards and every board — the Pocket Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { hex } from '../lib/colors'
import type { Board, HomeData } from '../lib/types'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

function BoardLink({ b, onNavigate }: { b: Board; onNavigate: () => void }) {
  return (
    <NavLink to={`/b/${b.id}`} onClick={onNavigate} className={({ isActive }) => `sb-nav-item sb-board ${isActive ? 'active' : ''}`} title={b.title}>
      <span className="sb-board-dot" style={{ background: hex(b.color) }}>{b.emoji}</span>
      <span className="ellipsis grow">{b.title}</span>
      {b.overdue > 0 && <span className="sb-count over" title={`${b.overdue} overdue`}>{b.overdue}</span>}
    </NavLink>
  )
}

export function Sidebar({ onCreate, onNavigate, home, onBoard }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; home: HomeData | null; onBoard: boolean }) {
  const { status, settings } = useApp()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Boards'
  const boards = (home?.boards || []).filter(b => !b.archived)
  const starred = boards.filter(b => b.starred)
  const mine = home ? home.counts.overdue + home.counts.today : 0
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: number; warn?: boolean }[] = [
    { to: '/', label: 'Home', icon: 'home', end: true },
    { to: '/my', label: 'My cards', icon: 'user', count: mine, warn: !!home?.counts.overdue },
    { to: '/trash', label: 'Bin', icon: 'trash' },
  ]
  return (
    <aside className="sidebar" aria-label="Boards">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Boards' ? title : <>Pocket <b>Boards</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={15} strokeWidth={2.2} />New<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate, onBoard)} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className={`sb-count ${n.warn ? 'over' : ''}`}>{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {starred.length > 0 && (
        <section className="sb-sec">
          <h4 className="sb-h"><span className="sb-h-btn"><Icon name="star" size={12} />Starred</span></h4>
          <div className="sb-boards">{starred.map(b => <BoardLink key={b.id} b={b} onNavigate={onNavigate} />)}</div>
        </section>
      )}
      <section className="sb-sec">
        <h4 className="sb-h"><span className="sb-h-btn">Boards</span><button type="button" className="sb-h-add" aria-label="New board" title="New board" onClick={() => onCreate('blank')}><Icon name="plus" size={14} /></button></h4>
        <div className="sb-boards">
          {boards.map(b => <BoardLink key={b.id} b={b} onNavigate={onNavigate} />)}
          {home && boards.length === 0 && <p className="sb-empty">No boards yet.</p>}
        </div>
      </section>

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pbo:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
