import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['g', 'h'], 'Home'], [['g', 'm'], 'My cards'], [['g', 'b'], 'Bin'], [[','], 'Settings'], [['⌘', 'K'], 'Search boards and cards']] },
  { title: 'On a board', keys: [[['n'], 'New card (in the first list)'], [['/'], 'Filter the cards'], [['f'], 'Show or hide the filter'], [['esc'], 'Close the card']] },
  { title: 'Adding a card', keys: [[['#'], 'Label: #urgent'], [['@'], 'Assign: @ana'], [['!'], 'Due: !today !fri !3d !17:00'], [['enter'], 'Add, and keep adding']] },
  { title: 'Window', keys: [[['n'], 'New board (off a board)'], [['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
