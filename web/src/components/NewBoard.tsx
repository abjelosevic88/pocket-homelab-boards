// New board: blank, from a template, or drafted by AI (then created from the draft).
import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { BOARD_COLORS, hex, inkOn } from '../lib/colors'
import type { AIStatus, Board, Template } from '../lib/types'
import { Icon } from './icons'
import { Swatches } from './bits'
import { CoreHint, Field, Loading, Modal, Segmented, Spinner, useAsync, useToast } from './ui'
import { useApp } from '../lib/app-context'

export type NewBoardMode = 'blank' | 'template' | 'ai'

const EMOJIS = ['', '📋', '🏠', '✅', '🚀', '🛒', '✈️', '🎯', '💡', '📚', '🛠️', '🎉', '💼', '🌱', '🍳', '🏋️']

export function NewBoardModal({ mode: initial, onClose, onCreated }: { mode: NewBoardMode; onClose: () => void; onCreated: (b: Board) => void }) {
  const { toast } = useToast()
  const { settings } = useApp()
  const [mode, setMode] = useState<NewBoardMode>(initial)
  const [title, setTitle] = useState('')
  const [color, setColor] = useState(BOARD_COLORS[0])
  const [emoji, setEmoji] = useState('')
  const [busy, setBusy] = useState(false)
  const [tpl, setTpl] = useState<Template | null>(null)
  const [goal, setGoal] = useState('')
  const [draft, setDraft] = useState<Template | null>(null)
  const [drafting, setDrafting] = useState(false)
  const templates = useAsync(() => api.get<Template[]>(`${V1}/templates`), [])
  const ai = useAsync(() => api.get<AIStatus>(`${V1}/ai/status`), [])
  const coreOn = !!settings?.core_configured && settings.core_registered !== false
  const aiReady = !!ai.data?.ready

  const create = async (e?: React.FormEvent) => {
    e?.preventDefault()
    const source = mode === 'template' ? tpl : mode === 'ai' ? draft : null
    const t = title.trim() || source?.title || ''
    if (!t) { toast('Give the board a name', 'err'); return }
    setBusy(true)
    try {
      const body: Record<string, unknown> = { title: t, color: source && !title.trim() ? source.color || color : color, emoji: emoji || source?.emoji || '' }
      if (mode === 'template' && tpl?.key) body.template = tpl.key
      if (mode === 'ai' && draft) body.template_data = draft
      onCreated(await api.post<Board>(`${V1}/boards`, body))
    } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  const runDraft = async () => {
    if (!goal.trim()) return
    setDrafting(true)
    try { const d = await api.post<Template>(`${V1}/ai/board`, { goal: goal.trim() }); setDraft(d); setTitle(d.title || ''); if (d.color) setColor(d.color.replace('#', '')); if (d.emoji) setEmoji(d.emoji) } catch (err) { toast((err as Error).message, 'err') } finally { setDrafting(false) }
  }

  const canCreate = mode === 'blank' ? !!title.trim() : mode === 'template' ? !!tpl : !!draft
  return (
    <Modal title="New board" onClose={onClose} size="lg" footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" onClick={() => create()} disabled={busy || !canCreate}>{busy ? 'Creating…' : 'Create board'}</button>
    </>}>
      <div className="stack">
        <Segmented value={mode} onChange={setMode} options={[
          { value: 'blank', label: 'Blank' }, { value: 'template', label: 'From a template' },
          { value: 'ai', label: 'Draft with AI', disabled: !ai.loading && !aiReady, title: aiReady ? undefined : 'Needs Pocket AI (through Pocket Core) or an OpenAI key in Settings → AI' },
        ]} />
        {!ai.loading && !aiReady && mode !== 'ai' && <p className="muted xsmall">Drafting with AI needs Pocket AI (through Pocket Core) or your own OpenAI key in Settings → AI.</p>}
        {!ai.loading && !aiReady && !coreOn && mode === 'ai' && <CoreHint />}

        {mode === 'template' && (templates.loading ? <Loading /> : (
          <div className="tpl-grid">
            {(templates.data || []).map(t => (
              <button key={t.key} type="button" className={`tpl ${tpl?.key === t.key ? 'on' : ''}`} onClick={() => { setTpl(t); setColor((t.color || color).replace('#', '')); setEmoji(t.emoji || '') }}
                style={{ '--board': hex(t.color) } as React.CSSProperties}>
                <span className="tpl-head" style={{ color: inkOn(t.color) }}>{t.emoji} {t.title}</span>
                <span className="tpl-desc">{t.description}</span>
                <span className="tpl-lists">{t.lists.map(l => <span key={l.title}>{l.title}</span>)}</span>
              </button>
            ))}
          </div>
        ))}

        {mode === 'ai' && (
          <div className="stack tight">
            <Field label="What is the board for?" help="A sentence or two: “Plan our move to a new flat in March”, “Launch the bakery’s website”. You can change everything afterwards.">
              <textarea rows={3} value={goal} onChange={e => setGoal(e.target.value)} placeholder="Plan a family trip to Lisbon in May" />
            </Field>
            <div className="row"><button type="button" className="btn" onClick={runDraft} disabled={drafting || !goal.trim() || !aiReady}>{drafting ? <Spinner /> : <Icon name="sparkles" size={15} />}{draft ? 'Draft again' : 'Draft the board'}</button></div>
            {draft && (
              <div className="draft">
                {draft.lists.map(l => (
                  <div key={l.title} className="draft-list">
                    <div className="draft-list-h">{l.title}{l.is_done && <span className="badge ok">done</span>}</div>
                    {l.cards.map((c, i) => <div key={i} className="draft-card">{c}</div>)}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        <form onSubmit={create} className="stack tight">
          <Field label="Name"><input value={title} onChange={e => setTitle(e.target.value)} placeholder={mode === 'blank' ? 'Home projects' : tpl?.title || draft?.title || 'Board name'} autoFocus={mode === 'blank'} maxLength={120} /></Field>
          <div className="row wrap top" style={{ gap: 18 }}>
            <Field group label="Colour"><Swatches value={color} onChange={setColor} colors={BOARD_COLORS} /></Field>
            <Field group label="Emoji"><div className="emoji-row">{EMOJIS.map(e => <button key={e || 'none'} type="button" className={`emoji-btn ${emoji === e ? 'on' : ''}`} onClick={() => setEmoji(e)} aria-label={e || 'No emoji'}>{e || <Icon name="x" size={12} />}</button>)}</div></Field>
          </div>
          {mode === 'blank' && <p className="muted xsmall">Starts with the lists To do, Doing and Done, and six labels.</p>}
        </form>
      </div>
    </Modal>
  )
}

export { EMOJIS }
