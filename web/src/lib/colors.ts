// The fixed palette for boards, labels and card covers (hex without #, as the API stores it).
export const PALETTE: { hex: string; name: string }[] = [
  { hex: '4bce97', name: 'Green' }, { hex: '1f845a', name: 'Dark green' }, { hex: '94c748', name: 'Lime' },
  { hex: 'f5cd47', name: 'Yellow' }, { hex: 'fea362', name: 'Orange' }, { hex: 'f87168', name: 'Red' },
  { hex: 'e774bb', name: 'Pink' }, { hex: '9f8fef', name: 'Purple' }, { hex: '579dff', name: 'Blue' },
  { hex: '0c66e4', name: 'Dark blue' }, { hex: '6cc3e0', name: 'Sky' }, { hex: '8590a2', name: 'Gray' },
]
export const BOARD_COLORS = ['0c66e4', '1f845a', '9f8fef', 'e774bb', 'fea362', 'f87168', '6cc3e0', '94c748', 'f5cd47', '8590a2']

export function hex(c: string | undefined, fallback = '8590a2'): string {
  const v = (c || '').replace(/^#/, '')
  return /^[0-9a-fA-F]{6}$/.test(v) ? `#${v}` : `#${fallback}`
}
function lum(c: string): number {
  const v = hex(c).slice(1)
  const ch = [0, 2, 4].map(i => parseInt(v.slice(i, i + 2), 16) / 255).map(x => (x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4))
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2]
}
/** Readable text on the colour: dark ink on light colours, white on dark ones. */
export function inkOn(c: string): string { return lum(c) > 0.32 ? '#172b4d' : '#ffffff' }
export function colorName(c: string): string { return PALETTE.find(p => p.hex === (c || '').replace(/^#/, '').toLowerCase())?.name || '' }
