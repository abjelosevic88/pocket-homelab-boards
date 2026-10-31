// A small, safe Markdown renderer for card descriptions and comments: headings, emphasis,
// code, links, quotes, lists and task lines (the card's checklist). Builds React nodes,
// never HTML strings.
import React from 'react'

/** The same task-line rule as the server's Checklist(): "- [ ] x", "* [x] x", "+ [X] x". */
export const TASK_RE = /^[ \t]*[-*+] \[([ xX])\](.*)$/
export function isTaskLine(line: string): boolean { const m = TASK_RE.exec(line); return !!m && line.trimStart().length >= 6 }

export interface TaskItem { index: number; line: number; done: boolean; text: string; depth: number }
/** Every task line of the description, in order (index = what PUT /checklist/{index} takes). */
export function tasks(desc: string): TaskItem[] {
  const out: TaskItem[] = []
  desc.split('\n').forEach((l, i) => {
    if (!isTaskLine(l)) return
    const m = TASK_RE.exec(l)!
    out.push({ index: out.length, line: i, done: m[1] !== ' ', text: m[2].trim(), depth: Math.floor((l.length - l.trimStart().length) / 2) })
  })
  return out
}
/** The description with one task line ticked or unticked. */
export function setTask(desc: string, index: number, done: boolean): string {
  const t = tasks(desc).find(x => x.index === index)
  if (!t) return desc
  const lines = desc.split('\n')
  lines[t.line] = lines[t.line].replace(/\[[ xX]\]/, done ? '[x]' : '[ ]')
  return lines.join('\n')
}
/** Append "- [ ] text" lines. */
export function addTasks(desc: string, items: string[]): string {
  const add = items.map(s => s.trim()).filter(Boolean).map(s => `- [ ] ${s}`)
  if (!add.length) return desc
  const body = desc.replace(/\s+$/, '')
  if (!body) return add.join('\n')
  const last = body.split('\n').pop() || ''
  return body + (isTaskLine(last) ? '\n' : '\n\n') + add.join('\n')
}
/** Remove a task line. */
export function removeTask(desc: string, index: number): string {
  const t = tasks(desc).find(x => x.index === index)
  if (!t) return desc
  const lines = desc.split('\n')
  lines.splice(t.line, 1)
  return lines.join('\n')
}

// ---- inline ----

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*|__[^_]+__)|(\*[^*\s][^*]*\*|_[^_\s][^_]*_)|(~~[^~]+~~)|(\[[^\]]+\]\((?:https?:\/\/|mailto:|\/)[^)\s]+\))|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g

export function inline(text: string, key = 'i'): React.ReactNode[] {
  const out: React.ReactNode[] = []
  let last = 0
  let n = 0
  for (const m of text.matchAll(INLINE)) {
    const i = m.index ?? 0
    if (i > last) out.push(text.slice(last, i))
    const s = m[0]
    const k = `${key}-${n++}`
    if (m[1]) out.push(<code key={k}>{s.slice(1, -1)}</code>)
    else if (m[2]) out.push(<strong key={k}>{inline(s.slice(2, -2), k)}</strong>)
    else if (m[3]) out.push(<em key={k}>{inline(s.slice(1, -1), k)}</em>)
    else if (m[4]) out.push(<del key={k}>{inline(s.slice(2, -2), k)}</del>)
    else if (m[5]) {
      const mm = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(s)!
      out.push(<a key={k} href={mm[2]} target="_blank" rel="noreferrer noopener">{inline(mm[1], k)}</a>)
    } else if (m[6]) out.push(<a key={k} href={s} target="_blank" rel="noreferrer noopener">{s}</a>)
    last = i + s.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

// ---- blocks ----

/** Renders Markdown. Task lines become checkboxes calling onToggle(index, done) (read-only without it). */
export function Markdown({ text, onToggle, className = '' }: { text: string; onToggle?: (index: number, done: boolean) => void; className?: string }) {
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  const taskAt = new Map<number, TaskItem>()
  for (const t of tasks(text)) taskAt.set(t.line, t)
  const blocks: React.ReactNode[] = []
  let i = 0
  let k = 0
  while (i < lines.length) {
    const line = lines[i]
    const key = `b${k++}`
    if (/^\s*$/.test(line)) { i++; continue }
    const fence = /^\s*(```|~~~)/.exec(line)
    if (fence) {
      const body: string[] = []
      i++
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++])
      i++
      blocks.push(<pre key={key} className="md-pre"><code>{body.join('\n')}</code></pre>)
      continue
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line)
    if (h) {
      const lvl = Math.min(h[1].length + 2, 6)
      blocks.push(React.createElement(`h${lvl}`, { key, className: 'md-h' }, inline(h[2], key)))
      i++
      continue
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) { blocks.push(<hr key={key} />); i++; continue }
    if (/^\s*>/.test(line)) {
      const body: string[] = []
      while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ''))
      blocks.push(<blockquote key={key}><Markdown text={body.join('\n')} /></blockquote>)
      continue
    }
    if (/^\s*([-*+]|\d+[.)])\s+/.test(line)) {
      const items: React.ReactNode[] = []
      const ordered = /^\s*\d+[.)]/.test(line)
      let j = 0
      while (i < lines.length && /^\s*([-*+]|\d+[.)])\s+/.test(lines[i])) {
        const l = lines[i]
        const depth = Math.floor((l.length - l.trimStart().length) / 2)
        const t = taskAt.get(i)
        const ik = `${key}-${j++}`
        if (t) {
          items.push(
            <li key={ik} className={`md-task ${t.done ? 'done' : ''}`} style={depth ? { marginLeft: depth * 18 } : undefined}>
              <label>
                <input type="checkbox" checked={t.done} disabled={!onToggle} onChange={e => onToggle?.(t.index, e.target.checked)} />
                <span>{inline(t.text, ik)}</span>
              </label>
            </li>)
        } else {
          items.push(<li key={ik} style={depth ? { marginLeft: depth * 18 } : undefined}>{inline(l.replace(/^\s*([-*+]|\d+[.)])\s+/, ''), ik)}</li>)
        }
        i++
      }
      blocks.push(ordered ? <ol key={key}>{items}</ol> : <ul key={key}>{items}</ul>)
      continue
    }
    const para: string[] = []
    while (i < lines.length && !/^\s*$/.test(lines[i]) && !/^\s*(```|~~~|#{1,6}\s|>|([-*+]|\d+[.)])\s+)/.test(lines[i])) para.push(lines[i++])
    if (!para.length) { para.push(lines[i++]) }
    blocks.push(<p key={key}>{para.flatMap((p, pi) => pi ? [<br key={`${key}br${pi}`} />, ...inline(p, `${key}-${pi}`)] : inline(p, `${key}-${pi}`))}</p>)
  }
  return <div className={`md ${className}`}>{blocks}</div>
}
