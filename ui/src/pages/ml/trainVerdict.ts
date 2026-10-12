import type { ModelVersion } from '@/lib/mlModels'

/** How a model compares with one that only guesses. */
export type Verdict = 'good' | 'fair' | 'weak'

const isNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/** An amount in the target's units, short: 12.4, 0.0123, 1,235. */
export function formatAmount(v: number): string {
  return Math.abs(v) >= 100 ? Math.round(v).toLocaleString() : String(Number(v.toPrecision(3)))
}

const outOf100 = (accuracy: number) => Math.round(accuracy * 100)

/** The typical error of a number model: mean absolute error, else RMSE. */
const typicalError = (m: Record<string, number>) => (isNumber(m.mae) ? m.mae : isNumber(m.rmse) ? m.rmse : undefined)

/**
 * What a version's score means, in one sentence: how often a classifier is
 * right, or how far off a number model typically is, on the held-back rows.
 * Null when its metrics have neither, as a custom script's may not.
 */
export function headline(v: ModelVersion): string | null {
  const m = v.metrics ?? {}
  if (v.task === 'classification' && isNumber(m.accuracy)) return `Right ${outOf100(m.accuracy)} of 100 times on rows it never saw`
  const off = v.task === 'regression' ? typicalError(m) : undefined
  return off === undefined ? null : `Typically off by ${formatAmount(off)} on rows it never saw`
}

/** What guessing alone scores, in the same words; null without a baseline. */
export function baselineSentence(v: ModelVersion): string | null {
  const b = v.baseline
  if (!b) return null
  if (v.task === 'classification' && isNumber(b.accuracy)) {
    return `Always guessing the most common answer is right ${outOf100(b.accuracy)} of 100 times.`
  }
  const off = v.task === 'regression' ? typicalError(b) : undefined
  return off === undefined ? null : `Always guessing the average is off by ${formatAmount(off)}.`
}

/**
 * Good, fair or weak: how much of the gap between guessing and a perfect
 * score the model closes. The score is accuracy or R², and both top out at 1.
 * Null when the server sent no baseline.
 */
export function verdict(v: ModelVersion): Verdict | null {
  const score = v.metrics?.score
  const base = v.baseline?.score
  if (!isNumber(score) || !isNumber(base) || base >= 1) return null
  const closed = (score - base) / (1 - base)
  if (closed >= 0.5) return 'good'
  if (closed >= 0.2) return 'fair'
  return 'weak'
}

export const VERDICT_TEXT: Record<Verdict, { label: string; color: string; meaning: string }> = {
  good: { label: 'Good', color: 'green', meaning: 'Much better than guessing.' },
  fair: { label: 'Fair', color: 'yellow', meaning: 'Better than guessing, with room to improve.' },
  weak: {
    label: 'Weak', color: 'red',
    meaning: 'Barely better than guessing. More rows, or columns that say more about the answer, usually help.',
  },
}
