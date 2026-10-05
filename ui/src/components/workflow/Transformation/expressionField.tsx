import { TextInput, type TextInputProps } from '@mantine/core'
import { FunctionPicker } from '@/components/shared/FunctionPicker'
import { applyFunction } from '@/lib/expressionInsert'

/**
 * The inputs of a node whose field is "a field or an expression": Mapping,
 * Data Conversion, Aggregate, Fuzzy Lookup, Term Extraction.
 *
 * These nodes read the field with evaluator.EvaluateField and write the result
 * to the target field, or to a name built from the field when there is none.
 * For a call there is no such name, and the engine refuses the node
 * (evaluator.OutputField). The two functions below restate that rule so the
 * editor can say it before a message does.
 */

/** Whether the engine reads `field` as a function call (evaluator.isCall). */
export const isCall = (field: string) => field.includes('(') && field.endsWith(')')

/** Where a node writes when no target field is set; null when it has nowhere. */
export function defaultOutputField(field: string, suffix = ''): string | null {
  if (!field || isCall(field)) return null
  return field.replace(/^source\./, '') + suffix
}

/** What to say when a call has no target field; nothing otherwise. */
export function missingTargetField(field: string | undefined, target: string | undefined): string | undefined {
  if (!field || target || !isCall(field)) return undefined
  return 'This expression has no field of its own to be written to. Name the field for the result.'
}

interface ExpressionFieldPickerProps {
  field: string | undefined
  /**
   * For a field whose result is written somewhere: the target as configured,
   * and the suffix the node adds to the field's name by default. Left out for
   * a field that is only read, such as a grouping key.
   */
  writesTo?: { targetField: string | undefined; suffix: string }
  onApply: (patch: { field: string; targetField?: string }) => void
}

/**
 * "Insert function" for a field-or-expression input. A function applies to the
 * whole field: `status` becomes `lower(source.status)`.
 *
 * When that turns a plain field into a call and no target is set, the target
 * becomes what the node was writing to a moment ago, so applying a function
 * changes what is read and not where it is written.
 */
export function ExpressionFieldPicker({ field, writesTo, onApply }: ExpressionFieldPickerProps) {
  const current = field || ''
  return (
    <FunctionPicker
      onPick={(fn) => {
        const next = applyFunction(current, current.length, current.length, fn, 'field').value
        const kept = writesTo && !writesTo.targetField ? defaultOutputField(current, writesTo.suffix) : null
        onApply(kept && isCall(next) ? { field: next, targetField: kept } : { field: next })
      }}
    />
  )
}

interface TargetFieldInputProps extends Omit<TextInputProps, 'value' | 'onChange' | 'error' | 'required'> {
  /** The field the node reads, which decides whether a target is needed. */
  field: string | undefined
  value: string | undefined
  /** What the node adds to the field's name when no target is set. */
  suffix?: string
  onChange: (value: string) => void
}

/**
 * The target field of such a node: optional for a plain field, with the
 * placeholder naming where the result goes when it is left blank, and required
 * for a call.
 */
export function TargetFieldInput({ field, value, suffix = '', onChange, ...rest }: TargetFieldInputProps) {
  const fallback = defaultOutputField(field || '', suffix)
  return (
    <TextInput
      label="Target Field"
      placeholder={fallback ? `Leave blank to write to ${fallback}` : 'Field to write the result to'}
      {...rest}
      value={value || ''}
      onChange={(e) => onChange(e.currentTarget.value)}
      required={isCall(field || '')}
      error={missingTargetField(field, value)}
    />
  )
}
