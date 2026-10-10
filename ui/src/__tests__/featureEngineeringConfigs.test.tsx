import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState, type ComponentType } from 'react'
import { describe, expect, it } from 'vitest'
import { ScaleConfig } from '@/components/workflow/Transformation/configs/features/ScaleConfig'
import { EncodeConfig } from '@/components/workflow/Transformation/configs/features/EncodeConfig'
import { BucketizeConfig } from '@/components/workflow/Transformation/configs/features/BucketizeConfig'
import { RollingConfig } from '@/components/workflow/Transformation/configs/features/RollingConfig'
import { AnomalyScoreConfig } from '@/components/workflow/Transformation/configs/features/AnomalyScoreConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

/**
 * The feature-engineering nodes: scale, encode, bucketize, rolling and
 * anomaly_score. Each editor saves the config pkg/comm/transformer/features
 * reads, so these tests assert the saved shape, not the markup.
 */

function renderEditor(Editor: ComponentType<any>, initial: Record<string, any> = {}) {
  let last: Record<string, any> = initial
  function Harness() {
    const [config, setConfig] = useState<Record<string, any>>(initial)
    return (
      <Editor
        nodeId="n1"
        config={config}
        fieldPaths={['amount', 'country', 'customer_id']}
        updateNodeConfig={(_id: string, patch: any) => {
          setConfig((c) => {
            const next = { ...c, ...patch }
            last = next
            return next
          })
        }}
      />
    )
  }
  render(
    <MantineProvider>
      <Harness />
    </MantineProvider>,
  )
  return () => last
}

describe('feature engineering nodes', () => {
  it('are offered in the palette under Feature Engineering, each with an editor', () => {
    const group = NODE_CATEGORIES.find((c) => c.title === 'Feature Engineering')
    const subTypes = group?.items.map((i: any) => i.subType)
    expect(subTypes).toEqual(['scale', 'encode', 'bucketize', 'rolling', 'anomaly_score'])
    expect(group?.group).toBe('transformations')
    expect(TRANSFORM_CONFIGS.scale).toBe(ScaleConfig)
    expect(TRANSFORM_CONFIGS.encode).toBe(EncodeConfig)
    expect(TRANSFORM_CONFIGS.bucketize).toBe(BucketizeConfig)
    expect(TRANSFORM_CONFIGS.rolling).toBe(RollingConfig)
    expect(TRANSFORM_CONFIGS.anomaly_score).toBe(AnomalyScoreConfig)
  })

  it('scale saves a field with its fitted statistics, and pasted stats', async () => {
    const config = renderEditor(ScaleConfig)
    const user = userEvent.setup()

    await user.click(screen.getByLabelText('Z-score'))
    expect(config().method).toBe('zscore')

    await user.click(screen.getByRole('button', { name: /add field/i }))
    await user.type(screen.getByRole('combobox', { name: /^field to scale/i }), 'amount')
    await user.type(screen.getByRole('textbox', { name: /^mean of amount/i }), '100')
    await user.type(screen.getByRole('textbox', { name: /^std of amount/i }), '50')
    await waitFor(() => expect(config().fields).toEqual([{ field: 'amount', mean: 100, std: 50 }]))

    const stats = screen.getByRole('textbox', { name: /fitted stats/i })
    await user.click(stats)
    await user.paste('{"age":{"mean":40,"std":10}}')
    expect(config().stats).toBe('{"age":{"mean":40,"std":10}}')
  })

  it('encode saves one-hot categories, then a hash bucket count', async () => {
    const config = renderEditor(EncodeConfig)
    const user = userEvent.setup()

    await user.type(screen.getByRole('combobox', { name: /^field/i }), 'country')
    expect(config().field).toBe('country')
    // One-hot is the default, here and in the backend.
    expect(screen.getByLabelText('One-hot')).toBeChecked()

    await user.type(screen.getByRole('combobox', { name: /^categories/i }), 'ID{enter}SG{enter}')
    expect(config().categories).toEqual(['ID', 'SG'])

    await user.click(screen.getByLabelText('Hash'))
    expect(config().method).toBe('hash')
    await user.type(screen.getByRole('textbox', { name: /^buckets/i }), '16')
    expect(config().buckets).toBe(16)
  })

  it('bucketize saves edges and labels, and says when they do not match', async () => {
    const config = renderEditor(BucketizeConfig)
    const user = userEvent.setup()

    await user.type(screen.getByRole('combobox', { name: /^field/i }), 'amount')
    await user.type(screen.getByRole('textbox', { name: /^edges/i }), '0, 100, 1000')
    expect(config().edges).toBe('0, 100, 1000')

    await user.type(screen.getByRole('combobox', { name: /^labels/i }), 'small{enter}')
    expect(config().labels).toEqual(['small'])
    expect(screen.getByText(/3 edges make 2 bins, but there is 1 label/i)).toBeInTheDocument()

    await user.type(screen.getByRole('combobox', { name: /^labels/i }), 'large{enter}')
    expect(config().labels).toEqual(['small', 'large'])
    expect(screen.queryByText(/edges make/i)).not.toBeInTheDocument()
  })

  it('rolling saves a per-key count window, then a time window', async () => {
    const config = renderEditor(RollingConfig)
    const user = userEvent.setup()

    await user.type(screen.getByRole('combobox', { name: /^field/i }), 'amount')
    await user.type(screen.getByRole('combobox', { name: /^key by/i }), 'customer_id')
    await user.type(screen.getByRole('textbox', { name: /^events in the window/i }), '20')
    expect(config()).toMatchObject({ field: 'amount', keyBy: 'customer_id', size: 20 })

    await user.click(screen.getByLabelText('Time window'))
    expect(config().windowType).toBe('time')
    await user.type(screen.getByRole('textbox', { name: /^window length/i }), '10m')
    expect(config().window).toBe('10m')

    await user.click(screen.getByRole('checkbox', { name: 'std' }))
    expect(config().features).toEqual(['count', 'sum', 'mean', 'min', 'max'])
  })

  it('anomaly score saves the method, threshold and window', async () => {
    const config = renderEditor(AnomalyScoreConfig)
    const user = userEvent.setup()

    await user.type(screen.getByRole('combobox', { name: /^field/i }), 'amount')
    await user.click(screen.getByLabelText('IQR'))
    expect(config().method).toBe('iqr')
    expect(screen.getByRole('textbox', { name: /^threshold/i })).toHaveAttribute('placeholder', '1.5')

    await user.type(screen.getByRole('textbox', { name: /^threshold/i }), '3')
    await user.type(screen.getByRole('textbox', { name: /^events in the window/i }), '50')
    expect(config()).toMatchObject({ field: 'amount', method: 'iqr', threshold: 3, size: 50 })
  })
})
