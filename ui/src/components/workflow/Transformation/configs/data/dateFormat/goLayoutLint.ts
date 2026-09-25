import { findDateFormat, type DateFormatGroup, type DateFormatOption } from './dateFormatOptions'

export interface LayoutProblem {
  message: string
  /** The layout that was meant, when there is exactly one reading of it. */
  fix?: { layout: string; label: string }
}

// The letter codes other date libraries use. None of these pairs occurs in a
// Go layout -- Go writes "Mon" and "Monday", never two d's or two M's in a row
// -- so any of them means the layout was written for another tool, and Go
// prints letters it does not recognise exactly as they are.
const LETTER_CODES = /YY|yy|DD|dd|MM|HH|hh|mm|ss/

// A four-digit year standing on its own. Inside a longer run of digits it is a
// compact layout such as "20060102150405", not a year.
const YEAR = /(?<!\d)(?:19|20)\d\d(?!\d)/g

/**
 * Finds the two mistakes a hand-written Go layout invites. Neither is an error
 * to Go: time.Format never fails, it just writes the wrong text into every row.
 *
 * - A real year where the reference year belongs: "02 January 2026" prints the
 *   day twice and a 6, "18 September 18186".
 * - Letter codes: "DD/MM/YYYY" prints "DD/MM/YYYY".
 *
 * The engine refuses a layout that prints no part of a date at all
 * (checkOutputFormat in pkg/comm/transformer/core/conversion.go); these two
 * are the ones it cannot see, because they do print something that changes.
 */
export function goLayoutProblem(layout: string, formats: DateFormatGroup[]): LayoutProblem | null {
  if (!layout.trim()) return null

  if (LETTER_CODES.test(layout)) {
    const listed = findByPattern(formats, layout)
    return {
      message:
        'This is written in letter codes, which Go does not read — it would print them as they are. ' +
        'Go layouts write the reference date Mon Jan 2 15:04:05 MST 2006 the way yours look: 02/01/2006 for DD/MM/YYYY.',
      fix: listed ? { layout: listed.layout, label: labelFor(listed.layout, listed) } : undefined,
    }
  }

  const wrongYear = [...layout.matchAll(YEAR)].map((m) => m[0]).find((year) => year !== '2006')
  if (wrongYear) {
    const fixed = layout.replace(YEAR, '2006')
    return {
      message: `“${wrongYear}” is not a year in a Go layout — Go writes the year as 2006, whatever the date.`,
      fix: { layout: fixed, label: labelFor(fixed, findDateFormat(formats, fixed)) },
    }
  }

  return null
}

function labelFor(layout: string, listed?: DateFormatOption): string {
  return listed ? `${layout} — ${listed.example}` : layout
}

// The listed format whose letter codes were typed. The second try reads Java's
// spelling (dd/MM/yyyy) as the one the list uses (DD/MM/YYYY); nothing else is
// guessed at, because a translation that is nearly right is a wrong date.
function findByPattern(formats: DateFormatGroup[], typed: string): DateFormatOption | undefined {
  const exact = typed.trim().replace(/\s+/g, ' ')
  const javaSpelling = exact.replace(/y/g, 'Y').replace(/d/g, 'D')
  for (const pattern of [exact, javaSpelling]) {
    for (const group of formats) {
      const found = group.items.find((o) => o.pattern === pattern)
      if (found) return found
    }
  }
  return undefined
}
