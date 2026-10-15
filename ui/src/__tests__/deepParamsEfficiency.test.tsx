import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { DeepParamsFields, readDeepParams, type DeepParamsText } from '@/pages/ml/deepParams'

/**
 * A network's efficiency settings: the precision its weights are kept in
 * and the CPU threads its training uses, sent as params.precision and
 * params.threads. Empty fields leave the engine's defaults (auto, and every
 * CPU but one).
 */
describe('deep-learning efficiency settings', () => {
  it('sends precision and threads when set, and nothing when empty', () => {
    expect(readDeepParams({ precision: 'int8', threads: '2' })).toEqual({ params: { precision: 'int8', threads: 2 }, errors: {} })
    expect(readDeepParams({ precision: '', threads: '' })).toEqual({ params: undefined, errors: {} })
    expect(readDeepParams({ threads: '1.5' }).errors.threads).toMatch(/whole number/i)
  })

  it('offers the precision and threads fields', async () => {
    function Harness() {
      const [value, setValue] = useState<DeepParamsText>({})
      return (
        <>
          <DeepParamsFields value={value} onChange={(patch) => setValue((v) => ({ ...v, ...patch }))} />
          <output data-testid="params">{JSON.stringify(readDeepParams(value).params ?? {})}</output>
        </>
      )
    }
    render(<MantineProvider><Harness /></MantineProvider>)
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox', { name: /training threads/i }), '3')
    await user.click(screen.getByRole('combobox', { name: /weights/i, hidden: true }))
    await user.click(await screen.findByRole('option', { name: 'float32', hidden: true }))
    expect(JSON.parse(screen.getByTestId('params').textContent!)).toEqual({ precision: 'float32', threads: 3 })
  })
})
