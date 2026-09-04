/**
 * Every icon-only button needs an accessible name.
 *
 * `<ActionIcon>` renders a <button> whose only content is an SVG, so without
 * aria-label a screen reader announces it as an unlabelled button — WCAG 2.2 AA
 * 4.1.2. A wrapping Mantine <Tooltip> does not supply one: it contributes
 * aria-describedby, which is a description, not a name.
 */
const sources = import.meta.glob('../**/*.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

/** Opening tags of ActionIcon elements, attributes included. */
function actionIconTags(code: string): string[] {
  const tags: string[] = []
  const re = /<ActionIcon\b/g
  let m: RegExpExecArray | null
  while ((m = re.exec(code))) {
    // Walk to the matching '>' of this opening tag, skipping over braces so a
    // '>' inside an arrow function or a generic does not end it early.
    let i = m.index + '<ActionIcon'.length
    let depth = 0
    let quote: string | null = null
    for (; i < code.length; i++) {
      const c = code[i]
      if (quote) {
        if (c === quote) quote = null
        continue
      }
      if (c === '"' || c === "'" || c === '`') { quote = c; continue }
      if (c === '{') depth++
      else if (c === '}') depth--
      else if (c === '>' && depth === 0) break
    }
    tags.push(code.slice(m.index, i + 1))
  }
  return tags
}

describe('icon button accessibility', () => {
  it('reads the components it is guarding', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(100)
  })

  it('gives every ActionIcon an accessible name', () => {
    const offenders: string[] = []
    for (const [file, code] of Object.entries(sources)) {
      if (file.includes('__tests__')) continue
      for (const tag of actionIconTags(code)) {
        if (!/\baria-label(?:ledby)?\s*=/.test(tag)) {
          offenders.push(`${file}: ${tag.replace(/\s+/g, ' ').slice(0, 90)}`)
        }
      }
    }
    expect(offenders).toEqual([])
  })

  /**
   * A bulk find-and-replace once gave every icon button one of a handful of
   * generic names. That is worse than it looks: the copy-to-clipboard buttons
   * announced "Confirm", the generate-password buttons announced "Refresh", and
   * ten delete buttons in a table all announced "Delete" with nothing to say
   * which row. A wrong name is not an improvement on a missing one.
   */
  it('uses no generic bulk-replace labels', () => {
    const generic = ['Confirm', 'Delete', 'Refresh', 'Toggle', 'Open in new tab', 'Add', 'Edit']
    const offenders: string[] = []
    for (const [file, code] of Object.entries(sources)) {
      if (file.includes('__tests__')) continue
      for (const label of generic) {
        if (code.includes(`aria-label="${label}"`)) offenders.push(`${file}: "${label}"`)
      }
    }
    expect(offenders).toEqual([])
  })
})
