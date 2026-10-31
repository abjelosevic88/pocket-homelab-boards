import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, upload, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, ConnMode, DeckBoard, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CopyField, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Spinner, Switch, Tabs, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'

type TabId = 'general' | 'notifications' | 'apps' | 'ai' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'general', label: 'General', icon: 'sliders', sub: 'Where the app opens, how cards look, dates and the default due time.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'Reminders before cards are due, when someone assigns you or comments, and a morning list.' },
  { id: 'apps', label: 'Connected apps', icon: 'layers', sub: 'Pocket Tasks and Pocket Time, the calendar feed, Nextcloud (to bring your Deck boards over) and Deck apps on your phone.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', sub: 'Who drafts boards and suggests checklist steps: Pocket AI through Core, or your own OpenAI key.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for scripts, Homepage widgets, Siri Shortcuts and Deck apps.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Bring boards over from Nextcloud Deck or Trello, export or import yours, download the database.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const tab: TabId = TABS.find(t => t.id === raw)?.id || 'general'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back" title="Back (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'general' && <General />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'apps' && <Integrations />}
            {tab === 'ai' && <AITab />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${(e as Error).message}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok' : test[which]?.startsWith('✕') ? 'bad' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

const HOURS = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: `${String(h).padStart(2, '0')}:00` }))

function General() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  const { themeMode, setThemeMode } = useApp()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Boards" icon="grid">
        <SettingRow title="Open with" help="What you see when you open the app."><Segmented size="sm" value={f.default_view} onChange={v => setF({ ...f, default_view: v })} options={[{ value: 'home', label: 'Home' }, { value: 'mine', label: 'My cards' }]} /></SettingRow>
        <SettingRow title="Cards" help="Compact fits more on the screen."><Segmented size="sm" value={f.card_density} onChange={v => setF({ ...f, card_density: v })} options={[{ value: 'comfortable', label: 'Comfortable' }, { value: 'compact', label: 'Compact' }]} /></SettingRow>
        <SettingRow title="Show done cards" help="Off: done cards are hidden on boards until you filter for them."><Switch checked={f.show_done_cards} onChange={v => setF({ ...f, show_done_cards: v })} label="Show done cards" /></SettingRow>
        <SettingRow title="Ask before deleting a card" help="Deleted cards go to the bin either way."><Switch checked={f.confirm_delete} onChange={v => setF({ ...f, confirm_delete: v })} label="Ask before deleting" /></SettingRow>
        <SettingRow title="Default due time" help="Used when you pick only a date."><input type="time" className="set-time" value={f.due_time_default} onChange={e => setF({ ...f, due_time_default: e.target.value })} aria-label="Default due time" /></SettingRow>
      </Section>
      <Section title="Dates and look" icon="calendar">
        <SettingRow title="Date format"><Select ariaLabel="Date format" value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select ariaLabel="Time format" value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Week starts on"><Segmented size="sm" value={f.week_start} onChange={v => setF({ ...f, week_start: v })} options={[{ value: 1, label: 'Monday' }, { value: 7, label: 'Sunday' }, { value: 6, label: 'Saturday' }]} /></SettingRow>
        <SettingRow title="Appearance" help="This device only."><Segmented size="sm" value={themeMode} onChange={setThemeMode} options={[{ value: 'system', label: 'System' }, { value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }]} /></SettingRow>
      </Section>
      {isAdmin && <Section title="This box" icon="server" sub="Admins only.">
        <SettingRow title="App name" help="Shown in the sidebar and the browser tab for everyone."><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} maxLength={60} /></SettingRow>
      </Section>}
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  const BEFORE = [{ value: '0', label: 'When it is due' }, { value: '15', label: '15 minutes before' }, { value: '60', label: '1 hour before' }, { value: '180', label: '3 hours before' }, { value: '1440', label: '1 day before' }, { value: '2880', label: '2 days before' }]
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Where notifications go: Pocket Alerts or an Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/boards" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
      </Section>
      <Section title="What to send" icon="clock" sub="Only about cards assigned to you — or, on cards nobody is assigned to, the boards you own.">
        <SettingRow title="Before a card is due">
          <div className="row wrap"><Switch checked={f.notify_due} onChange={v => setF({ ...f, notify_due: v })} label="Due reminders" />{f.notify_due && <Select ariaLabel="How long before" value={String(f.remind_before_min)} onChange={v => setF({ ...f, remind_before_min: Number(v) })} options={BEFORE} />}</div>
        </SettingRow>
        <SettingRow title="Someone assigns a card to you"><Switch checked={f.notify_assigned} onChange={v => setF({ ...f, notify_assigned: v })} label="Assigned" /></SettingRow>
        <SettingRow title="Comments on your cards" help="Cards you are assigned to or made yourself."><Switch checked={f.notify_comments} onChange={v => setF({ ...f, notify_comments: v })} label="Comments" /></SettingRow>
        <SettingRow title="Morning list" help="What is due today and what is overdue, once a day.">
          <div className="row wrap"><Switch checked={f.notify_digest} onChange={v => setF({ ...f, notify_digest: v })} label="Morning list" />{f.notify_digest && <Select ariaLabel="Hour" value={String(f.digest_hour)} onChange={v => setF({ ...f, digest_hour: Number(v) })} options={HOURS} />}</div>
        </SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        {status('notify')}
      </div>
    </div>
  )
}

function Integrations() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { toast } = useToast()
  const { settings, setSettings } = useApp()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const eff = (m: ConnMode): ConnMode => !coreOn && m === 'core' ? 'off' : m
  const ncMode = eff(f.nextcloud_mode)
  const rotate = async () => {
    try { const r = await api.post<{ ics_url: string }>(`${V1}/settings/ics-token`); setF({ ...f, ics_url: r.ics_url }); if (settings) setSettings({ ...settings, ics_url: r.ics_url }); toast('New address made — the old one no longer works') } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = f.base_url || window.location.origin
  return (
    <div className="stack loose">
      <div className="callout info"><Icon name="info" size={15} /><div>These connections are yours. Through <b>Pocket Core</b> Boards acts as you in your own Pocket apps with nothing to paste (switch it off for yourself in Pocket Core → Settings → Apps acting for me); <b>Direct</b> uses a URL and an API token you enter.{!coreOn && <CoreHint />}</div></div>
      <Section title="Pocket Tasks — send a card to your task list" icon="check-square" actions={connBadge(eff(f.tasks_mode), f.tasks_via)}
        sub="“Send to Pocket Tasks” on a card makes a task with the card’s title, due date and a link back here.">
        <Connection app="Pocket Tasks" mode={f.tasks_mode} via={f.tasks_via} detail={f.tasks_detail} core={coreOn} hasToken={!!f.tasks_token}
          onMode={m => save({ tasks_mode: m })} onDisconnect={() => save({ tasks_url: '', tasks_token: '' })}
          test={<><button type="button" className="btn sm" onClick={() => run('tasks', f.tasks_url, f.tasks_token)}>Test connection</button>{status('tasks')}</>}
          direct={<div className="two-col tight">
            <Field label="URL" help="Inside Docker use http://host.docker.internal:PORT or the service name."><input value={f.tasks_url} onChange={e => setF({ ...f, tasks_url: e.target.value })} placeholder="http://host.docker.internal:8184" /></Field>
            <Field label="API token" help="Pocket Tasks → Settings → API tokens"><input value={f.tasks_token} onChange={e => setF({ ...f, tasks_token: e.target.value })} autoComplete="off" /></Field>
          </div>} />
      </Section>
      <Section title="Pocket Time — track time on a card" icon="timer" actions={connBadge(eff(f.time_mode), f.time_via)}
        sub="“Start a timer” on a card starts your one running timer in Pocket Time, named after the card and its board.">
        <Connection app="Pocket Time" mode={f.time_mode} via={f.time_via} detail={f.time_detail} core={coreOn} hasToken={!!f.time_token}
          onMode={m => save({ time_mode: m })} onDisconnect={() => save({ time_url: '', time_token: '' })}
          test={<><button type="button" className="btn sm" onClick={() => run('time', f.time_url, f.time_token)}>Test connection</button>{status('time')}</>}
          direct={<div className="two-col tight">
            <Field label="URL"><input value={f.time_url} onChange={e => setF({ ...f, time_url: e.target.value })} placeholder="http://host.docker.internal:8219" /></Field>
            <Field label="API token" help="Pocket Time → Settings → API & shortcuts"><input value={f.time_token} onChange={e => setF({ ...f, time_token: e.target.value })} autoComplete="off" /></Field>
          </div>} />
      </Section>
      <Section title="Calendar feed" icon="calendar" sub="Every card with a due date on your boards, as a calendar you subscribe to — in Pocket Calendar, on your iPhone (Settings → Calendar → Accounts → Add subscribed calendar) or anywhere that reads .ics. Anyone with the address can read it, so keep it to yourself.">
        {f.ics_url ? <CopyField value={f.ics_url} label="Calendar feed address" secret /> : <p className="muted small">No address yet.</p>}
        <div className="row mt"><button type="button" className="btn sm" onClick={rotate}><Icon name="refresh" size={14} />{f.ics_url ? 'Make a new address' : 'Make an address'}</button></div>
      </Section>
      <Section title="Nextcloud — bring your Deck boards over" icon="cloud" actions={ncMode === 'off' ? <span className="badge">off</span> : f.nextcloud_configured ? <span className="badge ok">{ncMode === 'core' ? 'Pocket Core' : 'direct'}</span> : <span className="badge warn">not connected</span>}
        sub="Used only for importing (Backup & data → From Nextcloud Deck). Through Pocket Core it uses your own Nextcloud login from Core → My logins.">
        <div className="conn-block">
          <div className="conn-mode">
            <Segmented size="sm" value={ncMode} onChange={m => save({ nextcloud_mode: m as ConnMode })} options={[
              { value: 'core', label: 'Pocket Core', title: coreOn ? 'Your own Nextcloud login from Pocket Core (My logins)' : 'Needs Pocket Core: there is none on this box', disabled: !coreOn },
              { value: 'direct', label: 'Direct' }, { value: 'off', label: 'Off' }]} />
          </div>
          <div className={`conn-status ${ncMode === 'off' ? '' : f.nextcloud_configured ? 'ok' : 'bad'}`}><span className="dot" />{ncMode === 'off' ? 'Off.' : f.nextcloud_configured ? (ncMode === 'core' ? 'Your Nextcloud login from Pocket Core.' : 'Your Nextcloud address, user and app password.') : `Not connected${f.nextcloud_detail ? `: ${f.nextcloud_detail}` : ''}.`}</div>
          {ncMode === 'direct' && <div className="three-col tight">
            <Field label="URL"><input value={f.nextcloud_url} onChange={e => setF({ ...f, nextcloud_url: e.target.value })} placeholder="https://cloud.example.com" /></Field>
            <Field label="User"><input value={f.nextcloud_user} onChange={e => setF({ ...f, nextcloud_user: e.target.value })} autoComplete="off" /></Field>
            <Field label="App password" help="Nextcloud → Settings → Security → Devices & sessions"><input type="password" value={f.nextcloud_password} onChange={e => setF({ ...f, nextcloud_password: e.target.value })} autoComplete="new-password" /></Field>
          </div>}
          {ncMode !== 'off' && <div className="row wrap"><button type="button" className="btn sm" onClick={() => ncMode === 'direct' ? run('nextcloud', f.nextcloud_url, f.nextcloud_password, { user: f.nextcloud_user }) : run('nextcloud', '', '')}>Test connection</button>{status('nextcloud')}</div>}
        </div>
      </Section>
      <Section title="Deck apps on your phone or computer" icon="smartphone" sub="Apps made for Nextcloud Deck (for example Deck for Android, or iOS apps that speak the Deck API) work with Boards.">
        <ol className="steps">
          <li>In the app, choose to add a Nextcloud server and enter this address:<CopyField value={base} label="Server address" /></li>
          <li>The app opens a sign-in page here — choose <b>Allow</b>. The app gets its own key.</li>
          <li>If the app asks for a user and password instead: your email, and an API token from <b>API &amp; shortcuts</b> as the password.</li>
        </ol>
      </Section>
      <Section title="Other Pocket apps" icon="layers" sub="Boards offers your cards to the rest of the box through Pocket Core: Pocket Day can write what you finished into your journal, Pocket Calendar can show due cards, Pocket Notes or Pocket Flows can add cards.">
        <dl className="set-dl">
          <div><dt>boards:read</dt><dd className="muted">Your boards, lists and cards, what is due and what got done.</dd></div>
          <div><dt>boards:write</dt><dd className="muted">Add cards and change the ones that app added itself.</dd></div>
        </dl>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function AITab() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const { data: ai } = useAsync(() => api.get<{ ready: boolean; provider: string; detail: string }>(`${V1}/ai/status`), [])
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What the AI does" icon="sparkles" sub="Drafts a whole board from a sentence (“plan our move in March”) and suggests the steps of a card as checklist items. Only the board goal or the card’s title and description are sent, and only when you press the button.">
        {ai && (ai.ready ? <p className="muted small row" style={{ gap: 6 }}><Icon name="sparkles" size={13} />{ai.provider === 'pocket' ? 'Pocket AI' : 'Your OpenAI key'} is ready.</p>
          : <div className="callout info"><Icon name="info" size={15} /><div>No AI connected{ai.detail ? ` (${ai.detail})` : ''}.{!coreOn && <CoreHint />}</div></div>)}
      </Section>
      <Section title="Provider" icon="sparkles">
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Who writes" help="Automatic prefers your enabled Pocket AI connection; without one it uses your OpenAI settings.">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="platform.openai.com → API keys. Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col tight">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter…"><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the OpenAI key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

function Connection({ app, mode: saved, via, detail, core, onMode, onDisconnect, hasToken, test, direct }: {
  app: string; mode: ConnMode; via: string; detail: string; core: boolean; onMode: (m: ConnMode) => void; onDisconnect: () => void
  hasToken: boolean; test: React.ReactNode; direct: React.ReactNode
}) {
  const [advanced, setAdvanced] = useState(saved === 'direct')
  const mode: ConnMode = !core && saved === 'core' ? 'off' : saved
  const ok = via !== '' && mode !== 'off'
  const showDirect = !core || mode === 'direct' || advanced
  const line = mode === 'off' ? `Off: Boards does not talk to ${app}.`
    : mode === 'core' ? (ok ? `Connected through Pocket Core, as you${detail ? ` (${detail})` : ''}. Nothing to fill in; switch it off for yourself in Pocket Core → Settings → Apps acting for me.` : `Not connected: ${detail}.`)
    : (ok ? `Direct connection with your own ${app} URL and token; use Test connection to check it.` : `Not connected: ${detail || 'add the URL and a token'}.`)
  return (
    <div className="conn-block">
      <div className="conn-mode">
        <Segmented size="sm" value={mode} onChange={m => onMode(m as ConnMode)} options={[
          { value: 'core', label: 'Pocket Core', title: core ? 'Through Pocket Core: per person, nothing to paste' : 'Needs Pocket Core: there is none on this box', disabled: !core },
          ...(showDirect ? [{ value: 'direct', label: 'Direct', title: 'Your own URL and API token' }] : []),
          { value: 'off', label: 'Off' },
        ]} />
        {!showDirect && <button type="button" className="conn-advanced" onClick={() => setAdvanced(true)}>Advanced: connect directly with a URL and key</button>}
      </div>
      <div className={`conn-status ${mode === 'off' ? '' : ok ? 'ok' : 'bad'}`}><span className="dot" />{line}</div>
      {mode === 'direct' && <>
        {direct}
        <div className="row wrap">{test}{hasToken && <button type="button" className="btn sm ghost danger-text" onClick={onDisconnect} title="Remove the saved URL and token">Disconnect</button>}</div>
      </>}
      {mode === 'core' && <div className="row wrap">{test}</div>}
    </div>
  )
}

const connBadge = (mode: ConnMode, via: string) => mode === 'off' ? <span className="badge">off</span>
  : via ? <span className="badge ok">{via === 'core' ? 'Pocket Core' : 'direct'}</span>
  : <span className="badge warn">not connected</span>

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadMeta(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and has their own boards; they share boards with each other on the board itself. Admins manage accounts; they do not see anyone else’s boards.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted together with the boards they own. Boards others shared with them stay with their owners." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'User saved' : 'User added'); onSaved() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You">
        <div className="stack">
          <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>, or as the password of a Deck app (with your email as the user). A token acts as you: it can read and change all your boards. Deck apps that signed in through the browser are listed here too.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Deck on my phone)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Endpoints" icon="code" sub={<>At <code>{base}/api/v1</code> — see <code>docs/api.md</code>. A Nextcloud Deck–compatible API is at <code>{base}/index.php/apps/deck/api/v1.0</code>.</>}>
        <dl className="set-dl">
          <div><dt><code>POST /lists/ID/cards</code></dt><dd className="muted"><code>{'{"title":"Buy milk #shopping !tomorrow","quick":true}'}</code> — add a card (a Siri Shortcut).</dd></div>
          <div><dt><code>GET /cards?assigned=1&amp;due=today</code></dt><dd className="muted">Your cards due today — for Homepage or a widget.</dd></div>
          <div><dt><code>GET /home</code></dt><dd className="muted">Boards with their counters, and what is due.</dd></div>
          <div><dt><code>GET /overlay?from=&amp;to=</code></dt><dd className="muted">Due cards as calendar items — for Pocket Calendar.</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const done = () => window.dispatchEvent(new CustomEvent('pbo:changed'))
  const counts = (r: Record<string, number>) => Object.entries(r).filter(([, v]) => typeof v === 'number').map(([k, v]) => `${v} ${k}`).join(', ') || 'nothing new'
  const importJSON = async (file: File) => {
    setBusy(true)
    try { const r = await api.post<Record<string, number>>(`${V1}/import`, JSON.parse(await file.text())); toast('Imported: ' + counts(r)); done() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      <DeckImport onDone={done} />
      <TrelloImport onDone={done} />
      <Section title="Export & import" icon="archive" sub="Every board you own as one JSON file (lists, cards, labels, comments), or all cards as a CSV. Importing the same file twice adds nothing.">
        <div className="row wrap"><a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a><a className="btn" href={`${V1}/export.csv`}><Icon name="download" size={15} />Cards CSV</a><label className="btn"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Import JSON…'}<input type="file" accept="application/json,.json" hidden onChange={e => e.target.files?.[0] && importJSON(e.target.files[0])} /></label></div>
      </Section>
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). Files attached to cards live next to it in the data directory. Restore by stopping the app and replacing <code>pocket-boards.db</code>.</p>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>}
    </div>
  )
}

function DeckImport({ onDone }: { onDone: () => void }) {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const list = useAsync(() => open ? api.get<DeckBoard[]>(`${V1}/import/deck/boards`) : Promise.resolve([] as DeckBoard[]), [open])
  const [sel, setSel] = useState<Set<number>>(new Set())
  const [comments, setComments] = useState(true)
  const [files, setFiles] = useState(true)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState('')
  useEffect(() => { if (list.data) setSel(new Set(list.data.filter(b => !b.archived).map(b => b.id))) }, [list.data])
  const run = async () => {
    setBusy(true); setResult('')
    try {
      const r = await api.post<Record<string, number>>(`${V1}/import/deck`, { board_ids: [...sel], comments, attachments: files })
      setResult(Object.entries(r).map(([k, v]) => `${v} ${k}`).join(' · ')); toast('Deck boards imported'); onDone(); list.reload()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Section title="From Nextcloud Deck" icon="cloud" sub="Copies your Deck boards with their stacks, cards, labels, assignees, due dates, comments and files. Run it again any time: what was imported before is updated, never doubled. Your Deck stays as it is."
      actions={!open && <button type="button" className="btn primary sm" onClick={() => setOpen(true)}>Choose boards…</button>}>
      {open && (list.loading ? <Loading label="Asking Nextcloud…" /> : list.error ? <div className="callout warn"><Icon name="alert" size={15} /><div>{list.error}<br /><span className="muted small">Connect Nextcloud under Connected apps first.</span></div></div> : (
        <div className="stack tight">
          {(list.data || []).length === 0 ? <p className="muted">No Deck boards found.</p> : <div className="set-list">
            {(list.data || []).map(b => (
              <label key={b.id} className="check-row deck-row">
                <input type="checkbox" checked={sel.has(b.id)} onChange={() => setSel(s => { const n = new Set(s); if (n.has(b.id)) n.delete(b.id); else n.add(b.id); return n })} />
                <span className="sb-board-dot" style={{ background: `#${(b.color || '0c66e4').replace('#', '')}` }} />
                <span className="grow">{b.title}{b.archived && <span className="badge">archived</span>}</span>
                {b.imported_board_id ? <span className="badge ok">imported — will update</span> : null}
              </label>
            ))}
          </div>}
          <label className="check-row"><input type="checkbox" checked={comments} onChange={e => setComments(e.target.checked)} />Comments</label>
          <label className="check-row"><input type="checkbox" checked={files} onChange={e => setFiles(e.target.checked)} />Attached files</label>
          <div className="row wrap"><button type="button" className="btn primary" onClick={run} disabled={busy || !sel.size}>{busy ? <><Spinner />Importing…</> : `Import ${sel.size} board${sel.size === 1 ? '' : 's'}`}</button>{result && <span className="small ok">{result}</span>}</div>
        </div>
      ))}
    </Section>
  )
}

function TrelloImport({ onDone }: { onDone: () => void }) {
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState('')
  const run = async (file: File) => {
    setBusy(true); setResult('')
    try { const r = await upload<Record<string, number>>(`${V1}/import/trello`, 'file', file); setResult(Object.entries(r).map(([k, v]) => `${v} ${k}`).join(' · ')); toast('Trello board imported'); onDone() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Section title="From Trello" icon="upload" sub={<>In Trello open the board → Menu → Print, export and share → <b>Export as JSON</b>, then choose the file here. Lists, cards, labels, checklists (as checklist items), due dates and comments come along; files stay in Trello.</>}>
      <div className="row wrap"><label className="btn"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Choose a Trello JSON file…'}<input type="file" accept="application/json,.json" hidden onChange={e => { if (e.target.files?.[0]) run(e.target.files[0]); e.target.value = '' }} /></label>{result && <span className="small ok">{result}</span>}</div>
    </Section>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const core = (data.core || {}) as { configured?: boolean; registered?: boolean; url?: string; error?: string }
  const tiles: [string, number | undefined, string][] = [['Your boards', counts.boards, 'grid'], ['Shared with you', counts.shared_boards, 'users'], ['Cards', counts.cards, 'note'], ['Files', counts.attachments, 'link']]
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Pocket Core', core.configured ? (core.registered ? <span key="c" className="ok">connected · {core.url}</span> : <span key="c" className="bad">{core.error || 'not registered yet'}</span>) : <span key="c" className="muted">not configured</span>],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</span>],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Boards</h3>
          <p className="muted small">Boards, lists and cards — a Trello and Nextcloud Deck replacement: labels, due dates, checklists, comments, files, shared boards, repeating cards and a calendar feed. One Go binary, one SQLite file, one volume.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => (
          <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>
        ))}
      </div>
      <Section title="This server">
        <dl className="set-dl">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-boards" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Tasks, Time, Calendar, Day and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}
