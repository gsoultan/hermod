import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState, type ComponentType } from 'react'
import { describe, expect, it } from 'vitest'
import { MappingEditor } from '@/components/workflow/Transformation/MappingEditor'
import { AggregateConfig } from '@/components/workflow/Transformation/configs/data/AggregateConfig'
import { DataConversionConfig } from '@/components/workflow/Transformation/configs/data/DataConversionConfig'
import { FuzzyLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/FuzzyLookupConfig'
import { TermExtractionConfig } from '@/components/workflow/Transformation/configs/enrichment/TermExtractionConfig'
import { RateLimitConfig } from '@/components/workflow/Transformation/configs/logic/RateLimitConfig'

/**
 * Five nodes say their field is "a field or an expression", and write the
 * result to a name built from that field: Mapping back to the field, Fuzzy
 * Lookup to field_fuzzy. For lower(source.name) there is no such name, and the
 * engine now refuses the node (evaluator.OutputField) where it used to write
 * to a field named after the expression.
 *
 * So the editor has to make the working config the easy one: applying a
 * function to a plain field keeps writing where the node wrote before, and a
 * call with nowhere to go says so beside the field that fixes it. Two of the
 * five had no Target Field at all, so a call there could not be made to work.
 */

type Config = Record<string, any>
type User = ReturnType<typeof userEvent.setup>

function Harness({ Editor, initial, selectedNode }: { Editor: ComponentType<any>; initial: Config; selectedNode?: boolean }) {
  const [config, setConfig] = useState<Config>(initial)
  const updateNodeConfig = (_id: string, patch: Config) => setConfig((prev) => ({ ...prev, ...patch }))
  const fields = ['status', 'city', 'note', 'amount', 'region', 'user_id']
  return (
    <MantineProvider>
      {selectedNode ? (
        <Editor selectedNode={{ id: 'n1', data: config }} updateNodeConfig={updateNodeConfig} availableFields={fields} />
      ) : (
        <Editor config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" availableFields={fields} fieldPaths={fields} />
      )}
      <output data-testid="config">{JSON.stringify(config)}</output>
    </MantineProvider>
  )
}

const config = () => JSON.parse(screen.getByTestId('config').textContent || '{}')

/** Picks `search` from the function button of the input labelled `label`. */
async function applyFunction(user: User, label: RegExp, search: string, name: RegExp) {
  const input = screen.getByRole('combobox', { name: label })
  const wrapper = input.closest('.mantine-InputWrapper-root') as HTMLElement
  await user.click(within(wrapper).getByRole('button', { name: /insert function/i }))
  const picker = await screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
  await user.type(await within(picker).findByRole('textbox', { name: /search functions/i, hidden: true }), search)
  await user.click(within(picker).getByRole('button', { name, hidden: true }))
}

const NEEDS_TARGET = /no field of its own/i

describe('applying a function to a field keeps the node writing where it wrote', () => {
  it('Mapping: back to the field', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={MappingEditor} selectedNode initial={{ transType: 'mapping', field: 'status' }} />)

    await applyFunction(user, /source field/i, 'lower', /^insert lower\(/i)

    expect(config()).toMatchObject({ field: 'lower(source.status)', targetField: 'status' })
    expect(screen.queryByText(NEEDS_TARGET)).toBeNull()
  }, 20000)

  it('Fuzzy Lookup: to field_fuzzy', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={FuzzyLookupConfig} initial={{ transType: 'fuzzy_lookup', field: 'city' }} />)

    await applyFunction(user, /source field/i, 'lower', /^insert lower\(/i)

    expect(config()).toMatchObject({ field: 'lower(source.city)', targetField: 'city_fuzzy' })
  }, 20000)

  it('Term Extraction: to field_terms', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={TermExtractionConfig} initial={{ transType: 'term_extraction', field: 'note' }} />)

    await applyFunction(user, /source field/i, 'trim', /^insert trim\(/i)

    expect(config()).toMatchObject({ field: 'trim(source.note)', targetField: 'note_terms' })
  }, 20000)

  it('Aggregate: to field_type', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={AggregateConfig} initial={{ transType: 'aggregate', type: 'sum', field: 'amount' }} />)

    await applyFunction(user, /field to aggregate/i, 'toint', /^insert toInt\(/i)

    expect(config()).toMatchObject({ field: 'toInt(source.amount)', targetField: 'amount_sum' })
  }, 20000)

  it('Data Conversion: a row, back to its field', async () => {
    const user = userEvent.setup()
    render(
      <Harness
        Editor={DataConversionConfig}
        initial={{ transType: 'data_conversion', conversions: [{ field: 'amount', targetType: 'int' }] }}
      />
    )

    await applyFunction(user, /^field/i, 'trim', /^insert trim\(/i)

    expect(config().conversions).toEqual([{ field: 'trim(source.amount)', targetType: 'int', targetField: 'amount' }])
  }, 20000)

  it('leaves a target field that was already chosen', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={MappingEditor} selectedNode initial={{ transType: 'mapping', field: 'status', targetField: 'label' }} />)

    await applyFunction(user, /source field/i, 'lower', /^insert lower\(/i)

    expect(config()).toMatchObject({ field: 'lower(source.status)', targetField: 'label' })
  }, 20000)
})

describe('a call with nowhere to write its result', () => {
  it.each([
    ['Mapping', MappingEditor, true, { transType: 'mapping', field: 'lower(source.status)' }],
    ['Fuzzy Lookup', FuzzyLookupConfig, false, { transType: 'fuzzy_lookup', field: 'lower(source.city)' }],
    ['Term Extraction', TermExtractionConfig, false, { transType: 'term_extraction', field: 'trim(source.note)' }],
    ['Aggregate', AggregateConfig, false, { transType: 'aggregate', type: 'sum', field: 'toint(source.amount)' }],
    [
      'Data Conversion',
      DataConversionConfig,
      false,
      { transType: 'data_conversion', conversions: [{ field: 'trim(source.amount)', targetType: 'int' }] },
    ],
  ] as const)('%s says a target field is needed', (_name, Editor, selectedNode, initial) => {
    render(<Harness Editor={Editor} selectedNode={selectedNode} initial={initial} />)
    expect(screen.getAllByText(NEEDS_TARGET).length).toBeGreaterThan(0)
  })

  it.each([
    ['Mapping', MappingEditor, true, { transType: 'mapping', field: 'status' }],
    ['Mapping, with a target', MappingEditor, true, { transType: 'mapping', field: 'lower(source.status)', targetField: 'label' }],
    ['Fuzzy Lookup', FuzzyLookupConfig, false, { transType: 'fuzzy_lookup', field: 'city' }],
    ['Term Extraction', TermExtractionConfig, false, { transType: 'term_extraction', field: 'note' }],
    ['Aggregate', AggregateConfig, false, { transType: 'aggregate', type: 'sum', field: 'amount' }],
  ] as const)('%s says nothing when the result has somewhere to go', (_name, Editor, selectedNode, initial) => {
    render(<Harness Editor={Editor} selectedNode={selectedNode} initial={initial} />)
    expect(screen.queryByText(NEEDS_TARGET)).toBeNull()
  })
})

// Mapping and Fuzzy Lookup read a target field the editor never let anyone
// set, so the only place a call's result could go was the invented one.
describe('a target field can be set', () => {
  it.each([
    ['Mapping', MappingEditor, true, { transType: 'mapping', field: 'status' }, /leave blank to write to status$/i],
    ['Fuzzy Lookup', FuzzyLookupConfig, false, { transType: 'fuzzy_lookup', field: 'city' }, /leave blank to write to city_fuzzy$/i],
  ] as const)('%s', async (_name, Editor, selectedNode, initial, placeholder) => {
    const user = userEvent.setup()
    render(<Harness Editor={Editor} selectedNode={selectedNode} initial={initial} />)

    const target = screen.getByRole('textbox', { name: /target field/i })
    // The placeholder is where the engine writes when this is left blank.
    expect(target.getAttribute('placeholder')).toMatch(placeholder)
    await user.type(target, 'out')

    expect(config().targetField).toBe('out')
  })

  // The input showed "keywords" for a node that had no target set, while the
  // engine wrote to <field>_terms.
  it('Term Extraction shows where it really writes', () => {
    render(<Harness Editor={TermExtractionConfig} initial={{ transType: 'term_extraction', field: 'note' }} />)
    const target = screen.getByRole('textbox', { name: /target field/i })
    expect(target).toHaveValue('')
    expect(target.getAttribute('placeholder')).toMatch(/note_terms$/)
  })
})

// These two are only read, so a call needs nothing else set.
describe('a field that is only read', () => {
  it('Aggregate: the grouping key', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={AggregateConfig} initial={{ transType: 'aggregate', type: 'sum', field: 'amount', groupBy: 'region' }} />)

    await applyFunction(user, /group by key/i, 'lower', /^insert lower\(/i)

    expect(config()).toMatchObject({ groupBy: 'lower(source.region)', field: 'amount' })
    expect(config().targetField).toBeUndefined()
  }, 20000)

  it('Rate Limit: the key', async () => {
    const user = userEvent.setup()
    render(<Harness Editor={RateLimitConfig} initial={{ transType: 'rate_limit', keyField: 'user_id' }} />)

    await applyFunction(user, /key field/i, 'lower', /^insert lower\(/i)

    expect(config().keyField).toBe('lower(source.user_id)')
  }, 20000)
})
