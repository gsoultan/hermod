/**
 * The date formats a data_conversion row picks from, instead of typing a Go
 * layout.
 *
 * Go reads a layout by example — "02 January 2006" means day, month name, year
 * — so a layout is exactly the thing an operator mistypes: "02 January 2026"
 * is a valid layout that prints the day twice and never a year. Each entry is
 * therefore shown as the text it produces, not as its layout.
 *
 * Every example is a claim about what the engine writes or reads, and
 * `TestDataConversion_Date_EditorFormatsDoWhatTheyShow`
 * (pkg/comm/transformer/core/conversion_date_test.go) reads this file and checks
 * each one against the node itself. Keep every entry on one line, in the form
 * `{ layout: '…', example: '…', pattern: '…' }`.
 */

/** The instant every example renders. Day 18 and 16:30 keep day/month and 12/24-hour apart. */
export const EXAMPLE_INSTANT = '2026-09-18T16:30:57Z'

export interface DateFormatOption {
  /** The Go layout the row stores and the engine reads or writes with. */
  layout: string
  /** EXAMPLE_INSTANT in this layout — what the operator recognises. */
  example: string
  /** The same shape in the letter codes most other tools use. */
  pattern: string
}

export interface DateFormatGroup {
  group: string
  items: DateFormatOption[]
}

/**
 * How a value is read. ISO 8601 is read without a format, so this list is the
 * other shapes a source hands over — plus the ISO date, because that is what
 * the field's old placeholder put into many stored rows.
 */
export const INPUT_DATE_FORMATS: DateFormatGroup[] = [
  {
    group: 'Day first',
    items: [
      { layout: '02/01/2006', example: '18/09/2026', pattern: 'DD/MM/YYYY' },
      { layout: '02-01-2006', example: '18-09-2026', pattern: 'DD-MM-YYYY' },
      { layout: '02.01.2006', example: '18.09.2026', pattern: 'DD.MM.YYYY' },
      { layout: '02/01/2006 15:04:05', example: '18/09/2026 16:30:57', pattern: 'DD/MM/YYYY HH:mm:ss' },
      { layout: '02-01-2006 15:04', example: '18-09-2026 16:30', pattern: 'DD-MM-YYYY HH:mm' },
      { layout: '02 January 2006', example: '18 September 2026', pattern: 'DD MMMM YYYY' },
      { layout: '02 Jan 2006', example: '18 Sep 2026', pattern: 'DD MMM YYYY' },
      { layout: '02-Jan-2006', example: '18-Sep-2026', pattern: 'DD-MMM-YYYY' },
    ],
  },
  {
    group: 'Month first',
    items: [
      { layout: '01/02/2006', example: '09/18/2026', pattern: 'MM/DD/YYYY' },
      { layout: '01/02/2006 15:04:05', example: '09/18/2026 16:30:57', pattern: 'MM/DD/YYYY HH:mm:ss' },
      { layout: '01/02/2006 03:04:05 PM', example: '09/18/2026 04:30:57 PM', pattern: 'MM/DD/YYYY hh:mm:ss A' },
      { layout: 'January 2, 2006', example: 'September 18, 2026', pattern: 'MMMM D, YYYY' },
      { layout: 'Jan 2, 2006', example: 'Sep 18, 2026', pattern: 'MMM D, YYYY' },
    ],
  },
  {
    group: 'Year first',
    items: [
      { layout: '2006-01-02', example: '2026-09-18', pattern: 'YYYY-MM-DD' },
      { layout: '2006/01/02', example: '2026/09/18', pattern: 'YYYY/MM/DD' },
      { layout: '20060102', example: '20260918', pattern: 'YYYYMMDD' },
    ],
  },
  {
    group: 'Internet',
    items: [
      { layout: 'Mon, 02 Jan 2006 15:04:05 MST', example: 'Fri, 18 Sep 2026 16:30:57 UTC', pattern: 'RFC 1123' },
      { layout: 'Mon, 02 Jan 2006 15:04:05 -0700', example: 'Fri, 18 Sep 2026 16:30:57 +0000', pattern: 'RFC 1123, numeric zone' },
    ],
  },
]

/** How the converted value is written, when it is written as text. */
export const OUTPUT_DATE_FORMATS: DateFormatGroup[] = [
  {
    group: 'Date',
    items: [
      { layout: '2006-01-02', example: '2026-09-18', pattern: 'YYYY-MM-DD' },
      { layout: '02/01/2006', example: '18/09/2026', pattern: 'DD/MM/YYYY' },
      { layout: '01/02/2006', example: '09/18/2026', pattern: 'MM/DD/YYYY' },
      { layout: '02-01-2006', example: '18-09-2026', pattern: 'DD-MM-YYYY' },
      { layout: '02.01.2006', example: '18.09.2026', pattern: 'DD.MM.YYYY' },
      { layout: '20060102', example: '20260918', pattern: 'YYYYMMDD' },
      { layout: '02 January 2006', example: '18 September 2026', pattern: 'DD MMMM YYYY' },
      { layout: '02 Jan 2006', example: '18 Sep 2026', pattern: 'DD MMM YYYY' },
      { layout: 'January 2, 2006', example: 'September 18, 2026', pattern: 'MMMM D, YYYY' },
      { layout: 'Jan 2, 2006', example: 'Sep 18, 2026', pattern: 'MMM D, YYYY' },
      { layout: 'Monday, 02 January 2006', example: 'Friday, 18 September 2026', pattern: 'dddd, DD MMMM YYYY' },
    ],
  },
  {
    group: 'Date and time',
    items: [
      { layout: '2006-01-02 15:04:05', example: '2026-09-18 16:30:57', pattern: 'YYYY-MM-DD HH:mm:ss' },
      { layout: '2006-01-02 15:04', example: '2026-09-18 16:30', pattern: 'YYYY-MM-DD HH:mm' },
      { layout: '02/01/2006 15:04', example: '18/09/2026 16:30', pattern: 'DD/MM/YYYY HH:mm' },
      { layout: '01/02/2006 03:04 PM', example: '09/18/2026 04:30 PM', pattern: 'MM/DD/YYYY hh:mm A' },
      { layout: '02 January 2006 15:04', example: '18 September 2026 16:30', pattern: 'DD MMMM YYYY HH:mm' },
      { layout: '2006-01-02T15:04:05Z07:00', example: '2026-09-18T16:30:57Z', pattern: 'ISO 8601' },
      { layout: 'Mon, 02 Jan 2006 15:04:05 MST', example: 'Fri, 18 Sep 2026 16:30:57 UTC', pattern: 'RFC 1123' },
    ],
  },
  {
    group: 'Time',
    items: [
      { layout: '15:04:05', example: '16:30:57', pattern: 'HH:mm:ss' },
      { layout: '15:04', example: '16:30', pattern: 'HH:mm' },
      { layout: '03:04 PM', example: '04:30 PM', pattern: 'hh:mm A' },
    ],
  },
  {
    group: 'Month',
    items: [
      { layout: 'January 2006', example: 'September 2026', pattern: 'MMMM YYYY' },
      { layout: '2006-01', example: '2026-09', pattern: 'YYYY-MM' },
    ],
  },
]

/** The listed format with this layout, if there is one. */
export function findDateFormat(formats: DateFormatGroup[], layout: string): DateFormatOption | undefined {
  for (const group of formats) {
    const found = group.items.find((o) => o.layout === layout)
    if (found) return found
  }
  return undefined
}
