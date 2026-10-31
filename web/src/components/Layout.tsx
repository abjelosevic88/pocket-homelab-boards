// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { HomeData } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { NewBoardModal, type NewBoardMode } from './NewBoard'
import { Avatar, type Anchor, type MenuItem, Menu, MenuButton, modalOpen, rectOf, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pbo-sidebar'

/** Something was added or changed from the shell (or a page): the counts and the pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pbo:changed'))
export function useOnChange(cb: () => void) {
  const ref = useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('pbo:changed', h)
    return () => window.removeEventListener('pbo:changed', h)
  }, [])
}

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

/** The home data (every board with its counters, starred, recent, due) for the sidebar and pages. */
const HomeCtx = createContext<{ home: HomeData | null; reload: () => void }>({ home: null, reload: () => {} })
export const useHome = () => useContext(HomeCtx)

export type CreateKind = NewBoardMode | 'import' | 'card'

/** Open a create action from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pbo:create', { detail: { kind: k } }))

/** The "New" menu: the sidebar button and the phone's + button. */
export function createItems(onCreate: (k: CreateKind) => void, onBoard: boolean): MenuItem[] {
  return [
    ...(onBoard ? [{ label: 'New card', icon: 'plus', hint: 'N', onSelect: () => onCreate('card') }, 'sep' as const] : []),
    { label: 'Blank board', icon: 'grid', hint: onBoard ? undefined : 'N', onSelect: () => onCreate('blank') },
    { label: 'From a template', icon: 'layers', onSelect: () => onCreate('template') },
    { label: 'Draft with AI', icon: 'sparkles', onSelect: () => onCreate('ai') },
    'sep',
    { label: 'Import from Deck or Trello', icon: 'upload', onSelect: () => onCreate('import') },
  ]
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  const onBoard = /^\/b\/\d+/.test(loc.pathname)
  useEffect(() => { if (!onBoard) document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname, onBoard])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])

  const [home, setHome] = useState<HomeData | null>(null)
  const load = useCallback(() => { api.get<HomeData>(`${V1}/home`).then(setHome).catch(() => {}) }, [])
  useEffect(() => { load() }, [load])
  useOnChange(load)

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [newBoard, setNewBoard] = useState<NewBoardMode | null>(null)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  const create = (k: CreateKind) => {
    closeDrawer()
    if (k === 'import') navigate('/settings/data')
    else if (k === 'card') window.dispatchEvent(new CustomEvent('pbo:new-card'))
    else setNewBoard(k)
  }
  const createRef = useRef(create)
  createRef.current = create

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => { const d = (e as CustomEvent<{ kind: CreateKind }>).detail; createRef.current(d.kind) }
    window.addEventListener('pbo:shortcuts', open)
    window.addEventListener('pbo:create', make)
    return () => { window.removeEventListener('pbo:shortcuts', open); window.removeEventListener('pbo:create', make) }
  }, [])
  const gAt = useRef(0)
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop, .sheet-backdrop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const g = performance.now() - gAt.current < 1200
      gAt.current = 0
      if (g && e.key === 'h') return go('/')
      if (g && e.key === 'm') return go('/my')
      if (g && e.key === 'b') return go('/trash')
      switch (e.key) {
        case 'g': gAt.current = performance.now(); break
        case '/': e.preventDefault(); if (onBoard) window.dispatchEvent(new CustomEvent('pbo:filter-focus')); else setPalette(true); break
        case 'f': if (onBoard) { e.preventDefault(); window.dispatchEvent(new CustomEvent('pbo:filter-toggle')) } break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': e.preventDefault(); createRef.current(onBoard ? 'card' : 'blank'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'new', label: 'New board', icon: 'plus', keywords: 'create add blank', run: () => create('blank') },
    { id: 'tpl', label: 'New board from a template', icon: 'layers', keywords: 'create kanban weekly chores trip', run: () => create('template') },
    { id: 'ai', label: 'Draft a board with AI', icon: 'sparkles', keywords: 'plan generate', run: () => create('ai') },
    { id: 'home', label: 'Home', icon: 'home', hint: 'G H', run: () => navigate('/') },
    { id: 'my', label: 'My cards', icon: 'user', hint: 'G M', keywords: 'assigned due overdue today', run: () => navigate('/my') },
    { id: 'trash', label: 'Bin', icon: 'trash', keywords: 'deleted restore trash', run: () => navigate('/trash') },
    { id: 'import', label: 'Import from Nextcloud Deck or Trello', icon: 'upload', keywords: 'deck trello nextcloud', run: () => navigate('/settings/data') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'notify', label: 'Settings: notifications', icon: 'bell', keywords: 'reminders due digest push', run: () => navigate('/settings/notifications') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'tasks time calendar feed ics deck nextcloud core', run: () => navigate('/settings/apps') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <HomeCtx.Provider value={{ home, reload: load }}>
        <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''} ${onBoard ? 'board-route' : ''}`}>
          {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
          <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
            <Sidebar onCreate={create} onNavigate={closeDrawer} home={home} onBoard={onBoard} />
          </div>
          <div className="main">
            <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
            <main className="content"><div className="content-inner"><Outlet /></div></main>
          </div>
          {mobile && <button type="button" className="fab" aria-label={onBoard ? 'New card' : 'New board'} title={onBoard ? 'New card' : 'New board'} aria-haspopup={onBoard ? undefined : 'menu'} aria-expanded={onBoard ? undefined : !!fabMenu}
            onClick={e => { if (onBoard) create('card'); else setFabMenu(fabMenu ? null : rectOf(e.currentTarget)) }}><Icon name="plus" size={26} strokeWidth={2.2} /></button>}
          {fabMenu && <Menu anchor={fabMenu} align="right" items={createItems(create, false)} onClose={() => setFabMenu(null)} width={250} />}
          {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} boards={home?.boards || []} />}
          {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
          {newBoard && <NewBoardModal mode={newBoard} onClose={() => setNewBoard(null)} onCreated={b => { setNewBoard(null); announceChange(); navigate(`/b/${b.id}`) }} />}
        </div>
      </HomeCtx.Provider>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Home', '/'], my: ['My cards', '/my'], b: ['Boards', '/'], search: ['Search', '/search'], trash: ['Bin', '/trash'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pbo:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
