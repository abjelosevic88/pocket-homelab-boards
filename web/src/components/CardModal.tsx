// The card dialog: everything about one card, over its board (/b/:id/c/:cardId).
import React, { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError, uploadForm, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { hex, inkOn } from '../lib/colors'
import { ago, fmtBytes, fmtDue, fmtSpent, joinLocal, splitLocal } from '../lib/format'
import { addTasks, Markdown, setTask, tasks } from '../lib/markdown'
import type { AIStatus, Attachment, BoardView, Card, CardView, Comment, Label, Repeat, TimerResult, TimerState } from '../lib/types'
import { useHome } from './Layout'
import { Avatars, DueBadge, LabelChip, Swatches, describeActivity } from './bits'
import { Icon } from './icons'
import { Select } from './Select'
import { type Anchor, Avatar, Confirm, Empty, Loading, Modal, Popover, Segmented, Spinner, rectOf, useAsync, useToast } from './ui'

const REPEATS: { value: Repeat; label: string }[] = [
  { value: '', label: 'Does not repeat' }, { value: 'daily', label: 'Every day' }, { value: 'weekdays', label: 'Every weekday' },
  { value: 'weekly', label: 'Every week' }, { value: 'monthly', label: 'Every month' }, { value: 'yearly', label: 'Every year' },
]

type Pop = { kind: 'labels' | 'members' | 'dates' | 'cover' | 'move'; at: Anchor }

export function CardModal({ cardId, boardView, onClose, onChanged }: { cardId: number; boardView: BoardView; onClose: () => void; onChanged: () => void }) {
  const { toast } = useToast()
  const { settings, user } = useApp()
  const navigate = useNavigate()
  const [cv, setCv] = useState<CardView | null>(null)
  const [err, setErr] = useState('')
  const [pop, setPop] = useState<Pop | null>(null)
  const [confirmDel, setConfirmDel] = useState(false)
  const [dropping, setDropping] = useState(false)
  const [uploading, setUploading] = useState(0)
  const ai = useAsync(() => api.get<AIStatus>(`${V1}/ai/status`), [])

  const load = useCallback(async () => {
    try { setCv(await api.get<CardView>(`${V1}/cards/${cardId}`)); setErr('') }
    catch (e) { setErr(e instanceof ApiError && e.status === 404 ? 'This card does not exist any more, or it is not on a board you can see.' : (e as Error).message) }
  }, [cardId])
  useEffect(() => { load() }, [load])
  // the board changed underneath (someone else): refresh the card too
  const stamp = boardView.board.updated_at
  const first = useRef(true)
  useEffect(() => { if (first.current) { first.current = false; return } load() }, [stamp]) // eslint-disable-line react-hooks/exhaustive-deps

  const refresh = () => { load(); onChanged() }
  const put = async (patch: Partial<Card> & Record<string, unknown>, msg?: string) => {
    if (cv) setCv({ ...cv, card: { ...cv.card, ...patch } as Card })
    try { await api.put(`${V1}/cards/${cardId}`, patch); if (msg) toast(msg); refresh() } catch (e) { toast((e as Error).message, 'err'); load() }
  }
  const call = async (fn: () => Promise<unknown>, msg?: string) => { try { await fn(); if (msg) toast(msg); refresh() } catch (e) { toast((e as Error).message, 'err') } }

  const upload = async (files: FileList | File[]) => {
    const list = [...files]
    if (!list.length) return
    setUploading(n => n + list.length)
    for (const f of list) {
      try { await uploadForm<Attachment>(`${V1}/cards/${cardId}/attachments`, f) } catch (e) { toast(`${f.name}: ${(e as Error).message}`, 'err') }
      setUploading(n => n - 1)
    }
    refresh()
  }

  if (err) return <Modal title="Card" onClose={onClose} size="sm"><Empty icon="alert" title="Can’t open this card">{err}</Empty></Modal>
  if (!cv) return <Modal title="" onClose={onClose} size="lg" className="card-modal"><Loading /></Modal>

  const c = cv.card
  const canEdit = cv.board.role !== 'viewer'
  const labels = cv.labels
  const members = cv.members
  const cardLabels = c.labels.map(id => labels.find(l => l.id === id)).filter(Boolean) as Label[]
  const anchor = (e: React.MouseEvent, kind: Pop['kind']) => setPop({ kind, at: rectOf(e.currentTarget) })

  return (
    <Modal bare onClose={onClose} size="lg" className={`card-modal ${dropping ? 'dropping' : ''}`}>
      <div className="cm" onDragOver={e => { if (canEdit && e.dataTransfer.types.includes('Files')) { e.preventDefault(); setDropping(true) } }} onDragLeave={e => { if (e.currentTarget === e.target) setDropping(false) }}
        onDrop={e => { if (!canEdit) return; e.preventDefault(); setDropping(false); if (e.dataTransfer.files.length) upload(e.dataTransfer.files) }}
        onPaste={e => { if (!canEdit) return; const files = [...e.clipboardData.files]; if (files.length && !(e.target as HTMLElement).closest('textarea, input')) { e.preventDefault(); upload(files) } }}>
        {c.cover_color && <div className="cm-cover" style={{ background: hex(c.cover_color) }} />}
        <div className="cm-head">
          <button type="button" className={`cm-done ${c.done ? 'on' : ''}`} disabled={!canEdit} onClick={() => put({ done: !c.done }, c.done ? 'Reopened' : c.repeat ? 'Done — the next one is on the board' : 'Done')} aria-label={c.done ? 'Mark not done' : 'Mark done'} title={c.done ? 'Mark not done' : 'Mark done'}>
            <Icon name={c.done ? 'check-circle' : 'circle'} size={22} />
          </button>
          <div className="grow">
            <TitleEdit value={c.title} canEdit={canEdit} onSave={t => put({ title: t })} />
            <div className="cm-where muted small">
              in <button type="button" className="link-btn" disabled={!canEdit} onClick={e => anchor(e, 'move')}>{cv.list.title}</button>
              {cv.board.id !== boardView.board.id && <> on {cv.board.title}</>}
              {c.archived && <span className="badge warn">archived</span>}
              {c.source !== 'app' && c.source && <span className="faint"> · from {c.source === 'deck' ? 'Deck' : c.source}</span>}
            </div>
          </div>
          <button type="button" className="btn ghost icon-only" onClick={onClose} aria-label="Close" title="Close (esc)"><Icon name="x" size={18} /></button>
        </div>

        <div className="cm-cols">
          <div className="cm-main">
            <div className="cm-facts">
              {(members.length > 1 || c.members.length > 0) && <div className="cm-fact"><div className="cm-fact-h">Members</div><button type="button" className="cm-fact-v" disabled={!canEdit} onClick={e => anchor(e, 'members')}>
                {c.members.length ? <Avatars ids={c.members} members={members} size={26} max={6} /> : <span className="muted small">Nobody</span>}<Icon name="plus" size={14} className="muted" /></button></div>}
              <div className="cm-fact"><div className="cm-fact-h">Labels</div><button type="button" className="cm-fact-v" disabled={!canEdit} onClick={e => anchor(e, 'labels')}>
                {cardLabels.length ? <span className="cf-labels">{cardLabels.map(l => <LabelChip key={l.id} label={l} />)}</span> : <span className="muted small">None</span>}<Icon name="plus" size={14} className="muted" /></button></div>
              <div className="cm-fact"><div className="cm-fact-h">Due</div><button type="button" className="cm-fact-v" disabled={!canEdit} onClick={e => anchor(e, 'dates')}>
                {c.due_at ? <DueBadge due={c.due_at} done={c.done} /> : <span className="muted small">No date</span>}{c.repeat && <span className="cf-badge"><Icon name="repeat" size={12} />{REPEATS.find(r => r.value === c.repeat)?.label.toLowerCase()}</span>}</button></div>
              {(cv.time_spent_s || 0) > 0 && <div className="cm-fact"><div className="cm-fact-h">Time spent</div><div className="cm-fact-v static small" title={(cv.worklog || []).map(w => `${w.date}: ${fmtSpent(w.duration_s)}${w.source ? ` (${w.source})` : ''}`).join('\n')}><Icon name="timer" size={13} />{fmtSpent(cv.time_spent_s || 0)}</div></div>}
              {c.start_at && <div className="cm-fact"><div className="cm-fact-h">Starts</div><div className="cm-fact-v static small">{fmtDue(c.start_at)}</div></div>}
            </div>

            <Description card={c} canEdit={canEdit} aiReady={!!ai.data?.ready} onSave={d => put({ description: d })}
              onToggle={(i, done) => { if (cv) setCv({ ...cv, card: { ...c, description: setTask(c.description, i, done) } }); call(() => api.put(`${V1}/cards/${cardId}/checklist/${i}`, { done })) }} />

            <section className="cm-sec">
              <div className="cm-sec-h"><Icon name="link" size={16} /><h3>Files</h3>{uploading > 0 && <Spinner />}<span className="spacer" />
                {canEdit && <label className="btn sm"><Icon name="upload" size={14} />Add<input type="file" multiple hidden onChange={e => { if (e.target.files) upload(e.target.files); e.target.value = '' }} /></label>}</div>
              {cv.attachments.length === 0 ? <p className="muted small">{canEdit ? 'Drop files here, paste a screenshot, or use Add.' : 'No files.'}</p> : (
                <div className="att-list">{cv.attachments.map(a => <AttachmentRow key={a.id} a={a} canEdit={canEdit} onDelete={() => call(() => api.del(`${V1}/attachments/${a.id}`), 'File removed')} />)}</div>
              )}
            </section>

            <Comments cardId={cardId} comments={cv.comments} me={user?.id || 0} canComment={canEdit} onChanged={refresh} />

            <details className="cm-sec cm-activity">
              <summary className="cm-sec-h"><Icon name="activity" size={16} /><h3>Activity</h3><span className="muted small">{cv.activity.length}</span></summary>
              {cv.activity.map(a => <div key={a.id} className="act-row"><Avatar name={a.user_name} size={20} /><div className="grow"><div className="act-text"><b>{a.user_name || 'Someone'}</b> {describeActivity(a.kind, a.detail)}</div><div className="faint xsmall">{ago(a.created_at)}{a.via ? ` · via ${a.via === 'deck' ? 'a Deck app' : a.via}` : ''}</div></div></div>)}
            </details>
          </div>

          <aside className="cm-side">
            {canEdit && <>
              <div className="cm-side-h">Add to card</div>
              <button type="button" className="cm-act" onClick={e => anchor(e, 'members')}><Icon name="user" size={15} />Members</button>
              <button type="button" className="cm-act" onClick={e => anchor(e, 'labels')}><Icon name="tag" size={15} />Labels</button>
              <button type="button" className="cm-act" onClick={e => anchor(e, 'dates')}><Icon name="clock" size={15} />Dates</button>
              <button type="button" className="cm-act" onClick={e => anchor(e, 'cover')}><Icon name="palette" size={15} />Cover</button>
              <label className="cm-act"><Icon name="upload" size={15} />Attach a file<input type="file" multiple hidden onChange={e => { if (e.target.files) upload(e.target.files); e.target.value = '' }} /></label>
            </>}
            <div className="cm-side-h">Pocket apps</div>
            <PocketAction label="Send to Pocket Tasks" icon="check-square" ok={!!settings?.tasks_configured} done={!!c.task_ref} doneLabel="In Pocket Tasks" onRun={() => api.post<TimerResult>(`${V1}/cards/${cardId}/task`)} onDone={refresh} />
            <TimerAction cardId={cardId} ok={!!settings?.time_configured} onChanged={load} />
            {canEdit && <>
              <div className="cm-side-h">Card</div>
              <button type="button" className="cm-act" onClick={e => anchor(e, 'move')}><Icon name="move" size={15} />Move</button>
              <button type="button" className="cm-act" onClick={() => call(async () => { const n = await api.post<Card>(`${V1}/cards/${cardId}/copy`, {}); toast('Copied', 'ok', { label: 'Open', run: () => navigate(`/b/${n.board_id}/c/${n.id}`) }) })}><Icon name="copy" size={15} />Copy</button>
              <button type="button" className="cm-act" onClick={() => put({ archived: !c.archived }, c.archived ? 'Back on the board' : 'Archived')}><Icon name="archive" size={15} />{c.archived ? 'Unarchive' : 'Archive'}</button>
              <button type="button" className="cm-act danger" onClick={() => settings?.confirm_delete === false ? del() : setConfirmDel(true)}><Icon name="trash" size={15} />Delete</button>
            </>}
            <div className="cm-meta faint xsmall">Added {ago(c.created_at)}{new Date(c.updated_at).getTime() - new Date(c.created_at).getTime() > 2000 ? ` · changed ${ago(c.updated_at)}` : ''}</div>
          </aside>
        </div>
        {dropping && <div className="cm-drop"><Icon name="upload" size={22} />Drop to attach</div>}
      </div>

      {pop?.kind === 'labels' && <LabelsPop at={pop.at} cv={cv} onClose={() => setPop(null)} onChanged={refresh} />}
      {pop?.kind === 'members' && <Popover anchor={pop.at} onClose={() => setPop(null)} prefer="below" width={260} label="Members">
        <div className="pop-pad"><div className="pop-h">Members</div>
          {members.map(m => { const on = c.members.includes(m.user_id); return (
            <button key={m.user_id} type="button" className={`pick-row ${on ? 'on' : ''}`} onClick={() => call(() => on ? api.del(`${V1}/cards/${cardId}/members/${m.user_id}`) : api.put(`${V1}/cards/${cardId}/members/${m.user_id}`))}>
              <Avatar name={m.name} email={m.email} size={24} /><span className="grow ellipsis">{m.name || m.email}{m.user_id === user?.id ? ' (you)' : ''}</span>{on && <Icon name="check" size={15} />}
            </button>) })}
          {members.length < 2 && <p className="muted xsmall">Share the board (People, at the top) to assign cards to others.</p>}
        </div>
      </Popover>}
      {pop?.kind === 'dates' && <DatesPop at={pop.at} card={c} defTime={settings?.due_time_default || '17:00'} onClose={() => setPop(null)} onSave={p => { setPop(null); put(p) }} />}
      {pop?.kind === 'cover' && <Popover anchor={pop.at} onClose={() => setPop(null)} prefer="below" width={280} label="Cover"><div className="pop-pad stack tight"><div className="pop-h">Cover colour</div><Swatches value={c.cover_color} allowNone onChange={v => { setPop(null); put({ cover_color: v }) }} /></div></Popover>}
      {pop?.kind === 'move' && <MovePop at={pop.at} cv={cv} onClose={() => setPop(null)} onMove={p => { setPop(null); put(p, 'Moved') }} />}
      {confirmDel && <Confirm title={`Delete “${c.title}”?`} confirmLabel="Move to the bin" message="The card goes to the bin; you can bring it back from there for 30 days." onCancel={() => setConfirmDel(false)} onConfirm={() => { setConfirmDel(false); del() }} />}
    </Modal>
  )

  async function del() {
    try {
      await api.del(`${V1}/cards/${cardId}`)
      onChanged(); onClose()
      toast('Card moved to the bin', 'ok', { label: 'Undo', run: async () => { await api.post(`${V1}/cards/${cardId}/restore`); onChanged() } })
    } catch (e) { toast((e as Error).message, 'err') }
  }
}

function TitleEdit({ value, canEdit, onSave }: { value: string; canEdit: boolean; onSave: (t: string) => void }) {
  const [v, setV] = useState(value)
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => setV(value), [value])
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const fit = () => { el.style.height = 'auto'; el.style.height = el.scrollHeight + 'px' }
    fit()
    const ro = new ResizeObserver(fit)
    ro.observe(el)
    return () => ro.disconnect()
  }, [v])
  const save = () => { const t = v.trim(); if (t && t !== value) onSave(t); else setV(value) }
  if (!canEdit) return <h2 className="cm-title">{value}</h2>
  return <textarea ref={ref} className="cm-title cm-title-in" rows={1} value={v} onChange={e => setV(e.target.value.replace(/\n/g, ' '))} onBlur={save} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); (e.target as HTMLTextAreaElement).blur() } if (e.key === 'Escape') { e.stopPropagation(); setV(value); (e.target as HTMLTextAreaElement).blur() } }} aria-label="Title" maxLength={500} />
}

function Description({ card, canEdit, aiReady, onSave, onToggle }: { card: Card; canEdit: boolean; aiReady: boolean; onSave: (d: string) => void; onToggle: (i: number, done: boolean) => void }) {
  const { toast } = useToast()
  const [edit, setEdit] = useState(false)
  const [text, setText] = useState(card.description)
  const [item, setItem] = useState('')
  const [suggest, setSuggest] = useState<string[] | null>(null)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [asking, setAsking] = useState(false)
  useEffect(() => { if (!edit) setText(card.description) }, [card.description, edit])
  const ts = tasks(card.description)
  const done = ts.filter(t => t.done).length
  const add = () => { if (!item.trim()) return; onSave(addTasks(card.description, [item])); setItem('') }
  const ask = async () => {
    setAsking(true)
    try { const r = await api.post<{ items: string[] }>(`${V1}/ai/checklist`, { card_id: card.id }); setSuggest(r.items || []); setPicked(new Set((r.items || []).map((_, i) => i))) } catch (e) { toast((e as Error).message, 'err') } finally { setAsking(false) }
  }
  return (
    <>
      <section className="cm-sec">
        <div className="cm-sec-h"><Icon name="text" size={16} /><h3>Description</h3><span className="spacer" />{canEdit && !edit && <button type="button" className="btn sm" onClick={() => setEdit(true)}>Edit</button>}</div>
        {edit ? (
          <div className="stack tight">
            <textarea className="cm-desc-in" rows={Math.min(18, Math.max(6, text.split('\n').length + 1))} value={text} onChange={e => setText(e.target.value)} autoFocus
              placeholder={'Notes, links, and a checklist:\n- [ ] first step\n- [ ] second step'} onKeyDown={e => { if (e.key === 'Escape') { e.stopPropagation(); setText(card.description); setEdit(false) } if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { onSave(text); setEdit(false) } }} />
            <div className="row"><button type="button" className="btn primary sm" onClick={() => { onSave(text); setEdit(false) }}>Save</button><button type="button" className="btn ghost sm" onClick={() => { setText(card.description); setEdit(false) }}>Cancel</button><span className="muted xsmall">Markdown · <code>- [ ]</code> makes a checklist item · ⌘↵ saves</span></div>
          </div>
        ) : card.description.trim() ? <div className={canEdit ? 'cm-desc' : ''} onDoubleClick={() => canEdit && setEdit(true)}><Markdown text={card.description} onToggle={canEdit ? onToggle : undefined} /></div>
          : <button type="button" className="cm-desc-empty" disabled={!canEdit} onClick={() => setEdit(true)}>{canEdit ? 'Add a description…' : 'No description.'}</button>}
      </section>
      <section className="cm-sec">
        <div className="cm-sec-h"><Icon name="check-square" size={16} /><h3>Checklist</h3>{ts.length > 0 && <span className="muted small">{done}/{ts.length}</span>}<span className="spacer" />
          {canEdit && <button type="button" className="btn sm" disabled={!aiReady || asking} title={aiReady ? 'Suggest the steps with AI' : 'Needs Pocket AI (through Pocket Core) or an OpenAI key in Settings → AI'} onClick={ask}>{asking ? <Spinner /> : <Icon name="sparkles" size={14} />}Suggest steps</button>}</div>
        {ts.length > 0 && <div className="progress"><span style={{ width: `${Math.round(done / ts.length * 100)}%`, background: done === ts.length ? 'var(--success)' : undefined }} /></div>}
        {ts.length === 0 && <p className="muted xsmall">Items you add here are task lines in the description, so Deck apps see them too.</p>}
        {canEdit && <div className="row chk-add"><input value={item} onChange={e => setItem(e.target.value)} placeholder="Add an item" onKeyDown={e => { if (e.key === 'Enter') add() }} aria-label="New checklist item" /><button type="button" className="btn sm" onClick={add} disabled={!item.trim()}>Add</button></div>}
      </section>
      {suggest && <Modal title="Suggested steps" onClose={() => setSuggest(null)} size="sm" footer={<><button type="button" className="btn" onClick={() => setSuggest(null)}>Cancel</button><button type="button" className="btn primary" disabled={!picked.size} onClick={() => { onSave(addTasks(card.description, suggest.filter((_, i) => picked.has(i)))); setSuggest(null) }}>Add {picked.size} to the checklist</button></>}>
        {suggest.length === 0 ? <p className="muted">No suggestions this time.</p> : <div className="stack tight">{suggest.map((s, i) => (
          <label key={i} className="check-row"><input type="checkbox" checked={picked.has(i)} onChange={() => setPicked(p => { const n = new Set(p); if (n.has(i)) n.delete(i); else n.add(i); return n })} />{s}</label>
        ))}</div>}
      </Modal>}
    </>
  )
}

function AttachmentRow({ a, canEdit, onDelete }: { a: Attachment; canEdit: boolean; onDelete: () => void }) {
  const url = `${V1}/attachments/${a.id}`
  const img = a.mime.startsWith('image/')
  return (
    <div className="att">
      <a className="att-thumb" href={url} target="_blank" rel="noreferrer">{img ? <img src={url} alt="" loading="lazy" /> : <span className="att-ext">{(a.filename.split('.').pop() || 'file').slice(0, 4).toUpperCase()}</span>}</a>
      <div className="grow att-info"><a href={url} target="_blank" rel="noreferrer" className="ellipsis att-name">{a.filename}</a><div className="faint xsmall">{fmtBytes(a.size)} · {ago(a.created_at)}</div></div>
      <a className="btn ghost icon-only sm" href={`${url}?download=1`} aria-label="Download" title="Download"><Icon name="download" size={15} /></a>
      {canEdit && <button type="button" className="btn ghost icon-only sm" aria-label="Remove" title="Remove" onClick={onDelete}><Icon name="trash" size={15} /></button>}
    </div>
  )
}

function Comments({ cardId, comments, me, canComment, onChanged }: { cardId: number; comments: Comment[]; me: number; canComment: boolean; onChanged: () => void }) {
  const { toast } = useToast()
  const [text, setText] = useState('')
  const [edit, setEdit] = useState<{ id: number; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const post = async () => {
    if (!text.trim()) return
    setBusy(true)
    try { await api.post(`${V1}/cards/${cardId}/comments`, { message: text.trim() }); setText(''); onChanged() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const save = async () => { if (!edit) return; try { await api.put(`${V1}/comments/${edit.id}`, { message: edit.text }); setEdit(null); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  const del = async (id: number) => { try { await api.del(`${V1}/comments/${id}`); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <section className="cm-sec">
      <div className="cm-sec-h"><Icon name="note" size={16} /><h3>Comments</h3>{comments.length > 0 && <span className="muted small">{comments.length}</span>}</div>
      {canComment && <div className="cmt-new">
        <textarea rows={2} value={text} onChange={e => setText(e.target.value)} placeholder="Write a comment…" onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) post() }} aria-label="New comment" />
        {text.trim() && <div className="row"><button type="button" className="btn primary sm" onClick={post} disabled={busy}>Comment</button><span className="muted xsmall">⌘↵</span></div>}
      </div>}
      <div className="cmt-list">
        {[...comments].reverse().map(cm => (
          <div key={cm.id} className="cmt">
            <Avatar name={cm.author_name} size={26} />
            <div className="grow">
              <div className="cmt-head"><b>{cm.author_name || 'Someone'}</b><span className="faint xsmall">{ago(cm.created_at)}{new Date(cm.updated_at).getTime() - new Date(cm.created_at).getTime() > 2000 ? ' · edited' : ''}</span></div>
              {edit?.id === cm.id ? <div className="stack tight"><textarea rows={3} value={edit.text} onChange={e => setEdit({ ...edit, text: e.target.value })} autoFocus /><div className="row"><button type="button" className="btn primary sm" onClick={save}>Save</button><button type="button" className="btn ghost sm" onClick={() => setEdit(null)}>Cancel</button></div></div>
                : <div className="cmt-body"><Markdown text={cm.message} /></div>}
              {cm.user_id === me && edit?.id !== cm.id && <div className="cmt-acts"><button type="button" className="link-btn" onClick={() => setEdit({ id: cm.id, text: cm.message })}>Edit</button><button type="button" className="link-btn danger-text" onClick={() => del(cm.id)}>Delete</button></div>}
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}

function PocketAction({ label, icon, ok, done, doneLabel, onRun, onDone }: { label: string; icon: string; ok: boolean; done?: boolean; doneLabel?: string; onRun: () => Promise<TimerResult>; onDone?: () => void }) {
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    try {
      const r = await onRun()
      if (r.ok) { toast(`${label}: done`, 'ok', r.url ? { label: 'Open', run: () => window.open(r.url, '_blank', 'noopener') } : undefined); onDone?.() }
      else toast(r.error || 'That did not work', 'err')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <button type="button" className="cm-act" disabled={!ok || busy} onClick={run} title={ok ? undefined : 'Needs Pocket Core or a direct connection (Settings → Connected apps)'}>
      {busy ? <Spinner /> : <Icon name={icon} size={15} />}{done ? doneLabel : label}
    </button>
  )
}

/** Pocket Time: start the timer for this card, or stop it when it is running for this card.
 *  Another timer running elsewhere → offer to stop it and start this one. */
function TimerAction({ cardId, ok, onChanged }: { cardId: number; ok: boolean; onChanged: () => void }) {
  const { toast } = useToast()
  const [state, setState] = useState<TimerState | null>(null)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<TimerResult['running'] | null>(null)
  const refresh = useCallback(() => { if (ok) api.get<TimerState>(`${V1}/timer`).then(setState).catch(() => setState(null)) }, [ok])
  useEffect(() => { refresh() }, [refresh])
  const mine = !!state?.running && state.running.app_ref === `card:${cardId}`
  const post = async (body: Record<string, unknown>, okMsg: string) => {
    setBusy(true)
    try {
      const r = await api.post<TimerResult>(`${V1}/cards/${cardId}/timer`, body)
      if (r.ok) { toast(okMsg, 'ok', r.url ? { label: 'Open', run: () => window.open(r.url, '_blank', 'noopener') } : undefined); onChanged() }
      else if (r.conflict && r.running) setConflict(r.running)
      else toast(r.error || 'That did not work', 'err')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false); refresh() }
  }
  return (
    <>
      {mine ? (
        <button type="button" className="cm-act running" disabled={busy} onClick={() => post({ stop: true, only_if: state!.running!.id }, 'Timer stopped')} title={`Running since ${fmtDue(state!.running!.started_at)}`}>
          {busy ? <Spinner /> : <span className="rec-dot" />}Timer running — stop
        </button>
      ) : (
        <button type="button" className="cm-act" disabled={!ok || busy} onClick={() => post({}, 'Timer started')} title={ok ? undefined : 'Needs Pocket Core or a direct connection (Settings → Connected apps)'}>
          {busy ? <Spinner /> : <Icon name="timer" size={15} />}Start a timer
        </button>
      )}
      {conflict && <Confirm title="A timer is already running" confirmLabel="Stop it and start this" danger={false}
        message={<>Your timer is running for <b>{conflict.title || 'something else'}</b>{conflict.app_label ? ` (${conflict.app_label})` : ''}. Pocket Time runs one timer at a time.</>}
        onCancel={() => setConflict(null)} onConfirm={() => { setConflict(null); post({ switch: true }, 'Switched the timer to this card') }} />}
    </>
  )
}

function LabelsPop({ at, cv, onClose, onChanged }: { at: Anchor; cv: CardView; onClose: () => void; onChanged: () => void }) {
  const { toast } = useToast()
  const [edit, setEdit] = useState<{ id: number; title: string; color: string } | null>(null)
  const c = cv.card
  const call = async (fn: () => Promise<unknown>) => { try { await fn(); onChanged() } catch (e) { toast((e as Error).message, 'err') } }
  const saveEdit = async () => {
    if (!edit) return
    const e = edit
    setEdit(null)
    if (e.id) await call(() => api.put(`${V1}/labels/${e.id}`, { title: e.title.trim(), color: e.color }))
    else await call(async () => { const l = await api.post<Label>(`${V1}/boards/${c.board_id}/labels`, { title: e.title.trim(), color: e.color }); await api.put(`${V1}/cards/${c.id}/labels/${l.id}`) })
  }
  return (
    <Popover anchor={at} onClose={onClose} prefer="below" width={290} label="Labels">
      <div className="pop-pad">
        {edit ? (
          <div className="stack tight">
            <div className="pop-h">{edit.id ? 'Edit label' : 'New label'}</div>
            <span className="lbl lbl-preview" style={{ background: hex(edit.color), color: inkOn(edit.color) }}>{edit.title || ' '}</span>
            <input value={edit.title} onChange={e => setEdit({ ...edit, title: e.target.value })} placeholder="Name (optional)" autoFocus maxLength={60} onKeyDown={e => { if (e.key === 'Enter') saveEdit() }} />
            <Swatches value={edit.color} onChange={col => setEdit({ ...edit, color: col })} />
            <div className="row"><button type="button" className="btn primary sm" onClick={saveEdit}>Save</button><button type="button" className="btn ghost sm" onClick={() => setEdit(null)}>Back</button>
              {edit.id > 0 && <><span className="spacer" /><button type="button" className="btn ghost sm danger-text" onClick={() => { const id = edit.id; setEdit(null); call(() => api.del(`${V1}/labels/${id}`)) }}>Delete</button></>}</div>
          </div>
        ) : <>
          <div className="pop-h">Labels</div>
          {cv.labels.map(l => { const on = c.labels.includes(l.id); return (
            <div key={l.id} className="pick-label">
              <button type="button" className={`pick-check ${on ? 'on' : ''}`} aria-label={on ? 'Remove' : 'Add'} onClick={() => call(() => on ? api.del(`${V1}/cards/${c.id}/labels/${l.id}`) : api.put(`${V1}/cards/${c.id}/labels/${l.id}`))}>{on && <Icon name="check" size={13} />}</button>
              <button type="button" className="lbl lbl-wide" style={{ background: hex(l.color), color: inkOn(l.color) }} onClick={() => call(() => on ? api.del(`${V1}/cards/${c.id}/labels/${l.id}`) : api.put(`${V1}/cards/${c.id}/labels/${l.id}`))}>{l.title || ' '}</button>
              <button type="button" className="btn ghost icon-only sm" aria-label="Edit label" onClick={() => setEdit({ id: l.id, title: l.title, color: l.color })}><Icon name="edit" size={13} /></button>
            </div>) })}
          <button type="button" className="btn sm block mt" onClick={() => setEdit({ id: 0, title: '', color: '579dff' })}><Icon name="plus" size={14} />New label</button>
        </>}
      </div>
    </Popover>
  )
}

function DatesPop({ at, card, defTime, onClose, onSave }: { at: Anchor; card: Card; defTime: string; onClose: () => void; onSave: (p: Partial<Card>) => void }) {
  const due = splitLocal(card.due_at)
  const st = splitLocal(card.start_at)
  const [date, setDate] = useState(due.date)
  const [time, setTime] = useState(due.time || defTime)
  const [start, setStart] = useState(st.date)
  const [repeat, setRepeat] = useState<Repeat>(card.repeat)
  const quick = (days: number) => { const t = new Date(); t.setDate(t.getDate() + days); const p = (n: number) => String(n).padStart(2, '0'); setDate(`${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())}`) }
  return (
    <Popover anchor={at} onClose={onClose} prefer="below" width={300} label="Dates">
      <div className="pop-pad stack tight">
        <div className="pop-h">Due</div>
        <div className="row"><input type="date" value={date} onChange={e => setDate(e.target.value)} aria-label="Due date" /><input type="time" value={time} onChange={e => setTime(e.target.value)} aria-label="Due time" className="time-in" /></div>
        <div className="row wrap"><button type="button" className="chip" onClick={() => quick(0)}>Today</button><button type="button" className="chip" onClick={() => quick(1)}>Tomorrow</button><button type="button" className="chip" onClick={() => quick(7)}>In a week</button></div>
        <div className="pop-h">Starts (optional)</div>
        <input type="date" value={start} onChange={e => setStart(e.target.value)} aria-label="Start date" />
        <div className="pop-h">Repeat</div>
        <Select value={repeat} onChange={v => setRepeat(v as Repeat)} options={REPEATS.map(r => ({ value: r.value, label: r.label }))} ariaLabel="Repeat" />
        {repeat && <p className="muted xsmall">When you mark it done, the next one appears with the next due date.</p>}
        <div className="row"><button type="button" className="btn primary sm" onClick={() => onSave({ due_at: date ? joinLocal(date, time) : '', start_at: start ? joinLocal(start, '00:00') : '', repeat: date ? repeat : '' })}>Save</button>
          <button type="button" className="btn ghost sm" onClick={() => onSave({ due_at: '', start_at: '', repeat: '' })}>Remove dates</button></div>
      </div>
    </Popover>
  )
}

function MovePop({ at, cv, onClose, onMove }: { at: Anchor; cv: CardView; onClose: () => void; onMove: (p: Record<string, unknown>) => void }) {
  const { home } = useHome()
  const [boardId, setBoardId] = useState(String(cv.board.id))
  const [listId, setListId] = useState(String(cv.list.id))
  const [where, setWhere] = useState<'top' | 'bottom'>('bottom')
  const target = useAsync(() => Number(boardId) === cv.board.id ? Promise.resolve(null) : api.get<BoardView>(`${V1}/boards/${boardId}`), [boardId])
  const lists = Number(boardId) === cv.board.id ? null : target.data?.lists
  const [own, setOwn] = useState<BoardView | null>(null)
  useEffect(() => { api.get<BoardView>(`${V1}/boards/${cv.board.id}`).then(setOwn).catch(() => {}) }, [cv.board.id])
  const listOpts = (lists || own?.lists || []).map(l => ({ value: String(l.id), label: l.title }))
  useEffect(() => { if (listOpts.length && !listOpts.some(o => o.value === listId)) setListId(listOpts[0].value) }, [listOpts.map(o => o.value).join()]) // eslint-disable-line react-hooks/exhaustive-deps
  const boards = (home?.boards || []).filter(b => !b.archived && b.role !== 'viewer').map(b => ({ value: String(b.id), label: `${b.emoji ? b.emoji + ' ' : ''}${b.title}` }))
  return (
    <Popover anchor={at} onClose={onClose} prefer="below" width={300} label="Move">
      <div className="pop-pad stack tight">
        <div className="pop-h">Move card</div>
        <Select value={boardId} onChange={setBoardId} options={boards} ariaLabel="Board" />
        <Select value={listId} onChange={setListId} options={listOpts} ariaLabel="List" placeholder={target.loading ? 'Loading…' : 'List'} />
        <Segmented size="sm" value={where} onChange={setWhere} options={[{ value: 'top', label: 'At the top' }, { value: 'bottom', label: 'At the bottom' }]} />
        {Number(boardId) !== cv.board.id && <p className="muted xsmall">Labels go with it (made on the other board when missing); people who can’t see that board are taken off the card.</p>}
        <button type="button" className="btn primary sm" disabled={!listId} onClick={() => onMove({ board_id: Number(boardId), list_id: Number(listId), position: where === 'top' ? 0 : -1 })}>Move</button>
      </div>
    </Popover>
  )
}
