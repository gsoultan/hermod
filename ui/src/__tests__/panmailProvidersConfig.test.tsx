import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { PanmailProvidersConfig } from '@/components/workflow/Transformation/configs/enrichment/PanmailProvidersConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { guideFor } from '@/lib/transformationGuide'

// The keys written here are the ones pkg/comm/transformer/lookup/panmail_providers.go
// reads: baseUrl, apiKey, name, providerType, targetField, ttl.
describe('panmail_providers editor', () => {
  const renderConfig = (config: any, updateNodeConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <PanmailProvidersConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  it('writes the gateway url under baseUrl', async () => {
    const update = renderConfig({})
    await userEvent.type(screen.getByLabelText(/gateway url/i), 'h')
    expect(update).toHaveBeenLastCalledWith('n1', { baseUrl: 'h' })
  })

  it('hides the api key and writes it under apiKey', async () => {
    const update = renderConfig({})
    const input = screen.getByLabelText(/api key/i) as HTMLInputElement
    expect(input.type).toBe('password')
    await userEvent.type(input, 'k')
    expect(update).toHaveBeenLastCalledWith('n1', { apiKey: 'k' })
  })

  it('shows the default target field', () => {
    renderConfig({})
    expect((screen.getByLabelText(/target field/i) as HTMLInputElement).placeholder).toBe('panmail_providers')
  })

  it('is registered, offered in the palette, and has a guide', () => {
    expect(TRANSFORM_CONFIGS.panmail_providers).toBe(PanmailProvidersConfig)
    const offered = NODE_CATEGORIES.flatMap((c: any) => c.items).some((i: any) => i.subType === 'panmail_providers')
    expect(offered).toBe(true)
    expect(guideFor('panmail_providers').what).not.toBe('')
  })
})
