// Small pieces shared by the board, the card dialog and the lists of cards.
import React from 'react'
import { Link } from 'react-router-dom'
import { hex, inkOn, PALETTE } from '../lib/colors'
import { dueState, fmtDue } from '../lib/format'
import type { Board, Card, Label, Member } from '../lib/types'
import { Icon } from './icons'
import { Avatar } from './ui'

export function LabelChip({ label, compact = false, onClick }: { label: Label; compact?: boolean; onClick?: () => void }) {
  const bg = hex(label.color)
  if (compact && !label.title) return <span className="lbl lbl-bar" style={{ background: bg }} title={label.title || 'label'} />
  return <span className={`lbl ${onClick ? 'clickable' : ''}`} style={{ background: bg, color: inkOn(label.color) }} title={label.title} onClick={onClick}>{label.title || ' '}</span>
}

export function DueBadge({ due, done, withTime = true }: { due: string; done: boolean; withTime?: boolean }) {
  if (!due) return null
  const st = dueState(due, done)
  return <span className={`due due-${st}`} title={st === 'overdue' ? 'Overdue' : st === 'done' ? 'Done' : 'Due'}><Icon name={done ? 'check' : 'clock'} size={12} />{fmtDue(due, withTime)}</span>
}

export function Avatars({ ids, members, size = 22, max = 4 }: { ids: number[]; members: Member[]; size?: number; max?: number }) {
  const people = ids.map(id => members.find(m => m.user_id === id)).filter(Boolean) as Member[]
  if (!people.length) return null
  return (
    <span className="avatars">
      {people.slice(0, max).map(m => <span key={m.user_id} title={m.name || m.email}><Avatar name={m.name} email={m.email} size={size} /></span>)}
      {people.length > max && <span className="avatar-more" style={{ height: size, minWidth: size }}>+{people.length - max}</span>}
    </span>
  )
}

/** The colour swatches (hex without #); "" = none when allowNone. */
export function Swatches({ value, onChange, allowNone, colors }: { value: string; onChange: (c: string) => void; allowNone?: boolean; colors?: string[] }) {
  const list = colors || PALETTE.map(p => p.hex)
  return (
    <div className="swatches" role="radiogroup" aria-label="Colour">
      {allowNone && <button type="button" role="radio" aria-checked={!value} className={`swatch none ${!value ? 'active' : ''}`} title="None" onClick={() => onChange('')}><Icon name="x" size={12} /></button>}
      {list.map(c => <button key={c} type="button" role="radio" aria-checked={value === c} className={`swatch ${value === c ? 'active' : ''}`} title={PALETTE.find(p => p.hex === c)?.name || c} style={{ background: hex(c) }} onClick={() => onChange(c)} />)}
    </div>
  )
}

/** One card as it sits in a list (board view) or in a list of cards elsewhere. */
export function CardFace({ card, labels, members, compact, showBoard, dense }: { card: Card; labels: Label[]; members: Member[]; compact?: boolean; showBoard?: boolean; dense?: boolean }) {
  const ls = card.labels.map(id => labels.find(l => l.id === id)).filter(Boolean) as Label[]
  const hasBadges = card.due_at || card.checklist_total > 0 || card.comments > 0 || card.attachments > 0 || card.description || card.repeat || card.members.length > 0
  return (
    <>
      {card.cover_color && <span className="cf-cover" style={{ background: hex(card.cover_color) }} />}
      <div className="cf-body">
        {showBoard && <div className="cf-where"><span className="cf-board-dot" style={{ background: hex(card.board_color) }} />{card.board_title}<span className="faint"> · {card.list_title}</span></div>}
        {ls.length > 0 && <div className={`cf-labels ${dense ? 'dense' : ''}`}>{ls.map(l => <LabelChip key={l.id} label={l} compact={dense} />)}</div>}
        <div className={`cf-title ${card.done ? 'done' : ''}`}>{card.done && <Icon name="check-circle" size={14} className="cf-done-ico" />}{card.title}</div>
        {!compact && hasBadges && (
          <div className="cf-badges">
            <DueBadge due={card.due_at} done={card.done} />
            {card.repeat && <span className="cf-badge" title={`Repeats ${card.repeat}`}><Icon name="repeat" size={12} /></span>}
            {card.description && card.description.trim() && <span className="cf-badge" title="Has a description"><Icon name="text" size={12} /></span>}
            {card.checklist_total > 0 && <span className={`cf-badge ${card.checklist_done === card.checklist_total ? 'all' : ''}`} title="Checklist"><Icon name="check-square" size={12} />{card.checklist_done}/{card.checklist_total}</span>}
            {card.comments > 0 && <span className="cf-badge" title="Comments"><Icon name="note" size={12} />{card.comments}</span>}
            {card.attachments > 0 && <span className="cf-badge" title="Files"><Icon name="link" size={12} />{card.attachments}</span>}
            <span className="grow" />
            <Avatars ids={card.members} members={members} size={20} max={3} />
          </div>
        )}
      </div>
    </>
  )
}

/** A board tile for the home page. */
export function BoardTile({ board, onStar }: { board: Board; onStar?: (b: Board) => void }) {
  const c = hex(board.color)
  return (
    <Link to={`/b/${board.id}`} className="btile" style={{ '--board': c, color: inkOn(board.color) } as React.CSSProperties}>
      <div className="btile-top">
        {board.emoji && <span className="btile-emoji">{board.emoji}</span>}
        <span className="btile-title">{board.title}</span>
        {onStar && <button type="button" className={`btile-star ${board.starred ? 'on' : ''}`} aria-label={board.starred ? 'Unstar' : 'Star'} title={board.starred ? 'Unstar' : 'Star'}
          onClick={e => { e.preventDefault(); e.stopPropagation(); onStar(board) }}><Icon name="star" size={15} /></button>}
      </div>
      <div className="btile-meta">
        <span>{board.open} open</span>
        {board.overdue > 0 && <span className="btile-pill over">{board.overdue} overdue</span>}
        {board.due_soon > 0 && <span className="btile-pill soon">{board.due_soon} due soon</span>}
        {board.assigned > 0 && <span className="btile-pill">{board.assigned} mine</span>}
        {board.shared && <Icon name="users" size={13} />}
        {board.role === 'viewer' && <span className="btile-pill">view only</span>}
      </div>
    </Link>
  )
}

/** A short line saying what an activity row means. */
export function describeActivity(kind: string, d: Record<string, unknown> | null): string {
  const s = (k: string) => (d && typeof d[k] === 'string' ? (d[k] as string) : '')
  const t = s('title') || s('card')
  const q = t ? `“${t}”` : 'a card'
  const who = s('name') || s('user') || 'someone'
  const n = (k: string) => (d && typeof d[k] === 'number' ? (d[k] as number) : 0)
  switch (kind) {
    case 'card_created': return `added ${q}${s('list') ? ` to ${s('list')}` : ''}`
    case 'card_moved': return `moved ${q}${s('from') && s('to') ? ` from ${s('from')} to ${s('to')}` : s('to') ? ` to ${s('to')}` : ''}`
    case 'card_moved_board': return `moved ${q} to another board`
    case 'card_moved_in': return `moved ${q} here${s('from') ? ` from ${s('from')}` : ''}`
    case 'card_done': return `finished ${q}`
    case 'card_reopened': case 'card_undone': return `reopened ${q}`
    case 'card_repeated': return `${q} came round again`
    case 'card_archived': return `archived ${q}`
    case 'card_unarchived': return `brought back ${q}`
    case 'card_deleted': return `deleted ${q}`
    case 'card_purged': return `deleted ${q} for good`
    case 'card_restored': return `restored ${q}`
    case 'card_updated': {
      if (s('title_from')) return `renamed “${s('title_from')}” to ${q}`
      if (d && 'due_at' in d) return s('due_at') ? `set ${q} due ${fmtDue(s('due_at'))}` : `removed the due date of ${q}`
      if (d && d['description']) return `edited the description of ${q}`
      return `changed ${q}`
    }
    case 'checklist_complete': return `ticked off the whole checklist of ${q}`
    case 'card_assigned': return `gave ${q} to ${who}`
    case 'card_unassigned': return `took ${who} off ${q}`
    case 'comment': case 'comment_added': return `commented on ${q}`
    case 'attachment_added': return `attached ${s('filename') || 'a file'} to ${q}`
    case 'attachment_deleted': return `removed ${s('filename') || 'a file'} from ${q}`
    case 'sent_to_tasks': return `sent ${q} to Pocket Tasks`
    case 'timer_started': return `started the timer on ${q}`
    case 'time_logged': return `logged ${Math.round(n('duration_s') / 60)} min on ${q}`
    case 'list_created': return `added the list ${s('list')}`
    case 'list_updated': return `changed the list ${s('list')}`
    case 'list_deleted': return `deleted the list ${s('list')}`
    case 'list_restored': return `restored the list ${s('list')}`
    case 'list_sorted': return `sorted ${s('list')}`
    case 'list_archived_cards': return `archived every card in ${s('list')}`
    case 'board_created': return s('copied_from') ? `made this board from ${s('copied_from')}` : 'made this board'
    case 'board_updated': return 'changed the board'
    case 'board_archived': return 'archived the board'
    case 'board_unarchived': return 'brought the board back'
    case 'board_deleted': return 'deleted the board'
    case 'board_restored': return 'restored the board'
    case 'member_added': return `shared the board with ${who}`
    case 'member_role': return `changed what someone can do (${s('role')})`
    case 'member_removed': return 'stopped sharing with someone'
    case 'member_left': return 'left the board'
  }
  return kind.replace(/_/g, ' ') + (t ? ` ${q}` : '')
}
