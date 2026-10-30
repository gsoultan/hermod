import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { RedisLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/RedisLookupConfig'
import { RedisSetConfig } from '@/components/workflow/Transformation/configs/enrichment/RedisSetConfig'
import { configColumnSpan } from '@/lib/transformationLayout'

// The Redis steps sit in the transformation editor's middle column, about
// 360px wide on a 1280px screen. A two-field row there left the Key 116px wide,
// showing two characters of "customer:{{.after.customer_id}}". Every field in
// both forms takes the column's full width, and the column is the wide one.
const FIELDS = {
  lookup: ['Command', 'Key', 'Hash Field', 'Target Field (Message)', 'Value JSON Path'],
  lookupConnection: ['Address', 'Database', 'Username', 'Password'],
  lookupSettings: ['Default Value', 'Flatten Into', 'Cache TTL', 'Timeout'],
  set: ['Command', 'Key', 'Hash Field', 'Value'],
  setConnection: ['Address', 'Database', 'Username', 'Password'],
  setSettings: ['Expire After (TTL)', 'Timeout'],
}

// Measured from the field's wrapper, so a field's own inline buttons (the
// token pickers TemplateField puts beside its input) do not count as a row.
const sharesARow = (label: string) => {
  const input = screen.getAllByLabelText(new RegExp(`^${label.replace(/[()]/g, '\\$&')}`, 'i'))[0]
  const wrapper = input.closest('.mantine-InputWrapper-root') ?? input
  return wrapper.parentElement?.closest('.hermod-form-row, .mantine-Group-root') != null
}

const expectFullWidth = (labels: string[]) => {
  for (const label of labels) expect({ label, sharesARow: sharesARow(label) }).toEqual({ label, sharesARow: false })
}

const renderStep = (C: any, config: any) =>
  render(
    <MantineProvider>
      <C config={config} nodeId="n" updateNodeConfig={() => {}} />
    </MantineProvider>
  )

describe('Redis step fields have the column to themselves', () => {
  it('Redis Lookup', async () => {
    renderStep(RedisLookupConfig, { command: 'hget' })
    expectFullWidth(FIELDS.lookup)
    await userEvent.click(screen.getByRole('tab', { name: 'Connection' }))
    expectFullWidth(FIELDS.lookupConnection)
    await userEvent.click(screen.getByRole('tab', { name: 'Settings' }))
    expectFullWidth(FIELDS.lookupSettings)
  })

  it('Redis Set', async () => {
    renderStep(RedisSetConfig, { command: 'hset' })
    expectFullWidth(FIELDS.set)
    await userEvent.click(screen.getByRole('tab', { name: 'Connection' }))
    expectFullWidth(FIELDS.setConnection)
    await userEvent.click(screen.getByRole('tab', { name: 'Settings' }))
    expectFullWidth(FIELDS.setSettings)
  })
})

describe('configColumnSpan', () => {
  it('gives the Redis steps the wide column', () => {
    expect(configColumnSpan('redis_lookup')).toBe(6)
    expect(configColumnSpan('redis_set')).toBe(6)
  })

  it('keeps the existing widths', () => {
    for (const t of ['set', 'advanced', 'execute_sql']) expect(configColumnSpan(t)).toBe(6)
    expect(configColumnSpan('mask')).toBe(5)
  })
})
