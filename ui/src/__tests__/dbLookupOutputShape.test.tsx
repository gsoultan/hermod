import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { DBLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/DBLookupConfig'

// "Value Column(s)", "Target Field" and "Flatten Result" combine into four
// different output shapes, and nothing on screen said which one the current
// settings produce. Operators configured a multi-column lookup, looked for the
// columns at the top level of the message, and found an object one level down —
// or turned on Flatten and could not tell what the flattened paths were called.
//
// The panel therefore names the paths the next node can address.
describe('db_lookup output shape', () => {
  const renderConfig = (config: any) => {
    render(
      <MantineProvider>
        <DBLookupConfig
          config={config}
          updateNodeConfig={() => {}}
          nodeId="n1"
          availableFields={[]}
          sources={[{ id: 's1', name: 'db', type: 'postgres' }]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    fireEvent.click(screen.getByRole('tab', { name: /output mapping/i }))
    return screen.getByTestId('lookup-output-shape')
  }

  it('names the scalar path for a single column', () => {
    const shape = renderConfig({ sourceId: 's1', valueColumn: 'email', targetField: 'user_email' })
    expect(shape).toHaveTextContent('user_email')
    expect(shape).toHaveTextContent(/single value/i)
  })

  it('names the per-column paths for several columns', () => {
    const shape = renderConfig({ sourceId: 's1', valueColumn: 'email, name', targetField: 'user' })
    expect(shape).toHaveTextContent('user.email, user.name')
  })

  it('says the whole row is returned when no column is named', () => {
    const shape = renderConfig({ sourceId: 's1', valueColumn: '', targetField: 'user' })
    expect(shape).toHaveTextContent(/every column/i)
  })

  it('names the flattened paths when Flatten Result is on', () => {
    const shape = renderConfig({
      sourceId: 's1',
      valueColumn: 'email, name',
      targetField: 'user',
      flattenInto: '.',
    })
    expect(shape).toHaveTextContent('user.email, user.name')
    // Flattened to the top level, the columns are addressable by bare name.
    expect(shape).toHaveTextContent('Flattened to: email, name')
  })

  it('names the prefixed paths when Flatten Result targets a path', () => {
    const shape = renderConfig({
      sourceId: 's1',
      valueColumn: 'email, name',
      targetField: 'user',
      flattenInto: 'customer',
    })
    expect(shape).toHaveTextContent('Flattened to: customer.email, customer.name')
  })
})
