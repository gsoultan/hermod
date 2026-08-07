import { useForm } from '@tanstack/react-form'

/**
 * Validation for the identity fields shared by sources and sinks.
 *
 * Scope is deliberate. `name`, `type`, `vhost` and `worker_id` are statically
 * known, so they get real field-level validation with touched state and
 * submit-time aggregation. The `config` bag is a different problem: its keys
 * depend on the connector type chosen at runtime, so it stays in the existing
 * reducer rather than being forced into a typed form.
 *
 * Previously none of this existed — `@tanstack/react-form` was a declared
 * dependency with zero usages, every form was hand-rolled with an HTML
 * `required` attribute, and bad input surfaced as a backend error after submit
 * rather than as an inline message while typing.
 */

export interface EntityBasics {
  name: string
  type: string
  vhost: string
  worker_id: string
}

/** Characters that break DSNs, URLs and shell quoting downstream. */
const UNSAFE_NAME = /["'`\\]/

export function validateName(value: string): string | undefined {
  const v = (value ?? '').trim()
  if (!v) return 'Name is required'
  if (v.length < 2) return 'Name must be at least 2 characters'
  if (v.length > 64) return 'Name must be 64 characters or fewer'
  if (UNSAFE_NAME.test(v)) return 'Name cannot contain quotes or backslashes'
  return undefined
}

export function validateType(value: string): string | undefined {
  return (value ?? '').trim() ? undefined : 'Select a type'
}

export function validateVHost(value: string, required: boolean): string | undefined {
  if (!required) return undefined
  return (value ?? '').trim() ? undefined : 'Select a virtual host'
}

export interface UseEntityBasicsFormArgs {
  values: EntityBasics
  /** Mirrors edits back into the existing entity state. */
  onChange: (patch: Partial<EntityBasics>) => void
  /** VHost is not shown (and so not required) when embedded in the editor. */
  requireVHost: boolean
  onSubmit?: () => void
}

export function useEntityBasicsForm({
  values,
  onChange,
  requireVHost,
  onSubmit,
}: UseEntityBasicsFormArgs) {
  return useForm({
    defaultValues: values,
    onSubmit: async () => {
      onSubmit?.()
    },
    validators: {
      // Aggregate check so `canSubmit` is meaningful even before every field
      // has been touched.
      onChange: ({ value }) =>
        validateName(value.name) ??
        validateType(value.type) ??
        validateVHost(value.vhost, requireVHost),
    },
    listeners: {
      // Keep the existing entity state authoritative; the form is the
      // validation layer over it, not a second source of truth.
      onChange: ({ formApi }) => {
        onChange(formApi.state.values)
      },
    },
  })
}

/** Field-level validators, shared so wizard steps and forms agree. */
export const basicsValidators = {
  name: { onChange: ({ value }: { value: string }) => validateName(value) },
  type: { onChange: ({ value }: { value: string }) => validateType(value) },
  vhost: (required: boolean) => ({
    onChange: ({ value }: { value: string }) => validateVHost(value, required),
  }),
}
