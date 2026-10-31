import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { RedisSetSinkConfig } from '@/components/workflow/Sink/RedisSetSinkConfig'
import { RedisSetConfig } from '@/components/workflow/Transformation/configs/enrichment/RedisSetConfig'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { missingConnectionFields } from '@/lib/connectorRequirements'
import { guideFor } from '@/lib/transformationGuide'

// Both forms write the keys pkg/infra/rediskv.ParseSettings reads: addr,
// username, password, db, tls, command, key, field, value, ttl, onlyIfAbsent.
// The sink stores every value as text; the transformation stores db as a
// number and the switches as booleans, which redis_set.go reads either way.
describe('Redis Set sink form', () => {
  const renderSink = (config: any, updateConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <RedisSetSinkConfig config={config} updateConfig={updateConfig} />
      </MantineProvider>
    )
    return updateConfig
  }

  it('writes the address and the key', async () => {
    const update = renderSink({})
    await userEvent.type(screen.getByLabelText(/^address/i), 'r')
    expect(update).toHaveBeenLastCalledWith('addr', 'r')
    await userEvent.type(screen.getByLabelText(/^key/i), 'k')
    expect(update).toHaveBeenLastCalledWith('key', 'k')
  })

  it('stores the switches and the database as text', async () => {
    const update = renderSink({})
    await userEvent.click(screen.getByLabelText(/use tls/i))
    expect(update).toHaveBeenLastCalledWith('tls', 'true')
    await userEvent.type(screen.getByLabelText(/^database/i), '2')
    expect(update).toHaveBeenLastCalledWith('db', '2')
  })

  it('asks for a hash field only for HSET, and offers only-if-absent only for SET', () => {
    renderSink({ command: 'hset' })
    expect(screen.getByLabelText(/^hash field/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/only if the key is absent/i)).toBeNull()
  })

  it('hides the password', () => {
    renderSink({})
    expect((screen.getByLabelText(/^password/i) as HTMLInputElement).type).toBe('password')
  })

  it('is offered, has its own form, and is gated on address and key', () => {
    expect(SINK_TYPES.some((t) => t.value === 'redis_set')).toBe(true)
    expect(configComponents.redis_set).toBeDefined()
    expect(missingConnectionFields('sink', 'redis_set', {})).toEqual(['Address', 'Key'])
    const offered = NODE_CATEGORIES.flatMap((c: any) => c.items).some((i: any) => i.type === 'sink' && i.subType === 'redis_set')
    expect(offered).toBe(true)
  })
})

describe('Redis Set transformation form', () => {
  const renderStep = (config: any, updateNodeConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <RedisSetConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  it('writes the key under key', async () => {
    const update = renderStep({})
    await userEvent.type(screen.getByLabelText(/^key/i), 'k')
    expect(update).toHaveBeenLastCalledWith('n1', { key: 'k' })
  })

  it('writes the connection on its own tab', async () => {
    const update = renderStep({})
    await userEvent.click(screen.getByRole('tab', { name: /connection/i }))
    await userEvent.type(screen.getByLabelText(/^address/i), 'r')
    expect(update).toHaveBeenLastCalledWith('n1', { addr: 'r' })
  })

  it('fails the record on a failed write unless told to pass it through', async () => {
    const update = renderStep({})
    await userEvent.click(screen.getByRole('tab', { name: /settings/i }))
    const policy = screen.getByRole('combobox', { name: /when the write fails/i }) as HTMLInputElement
    expect(policy.value).toMatch(/fail/i)
    await userEvent.click(policy)
    await userEvent.click(screen.getByText(/pass the record on/i))
    expect(update).toHaveBeenCalledWith('n1', { onError: 'passthrough' })
  })

  it('is registered, offered in the palette, and has a guide', () => {
    expect(TRANSFORM_CONFIGS.redis_set).toBe(RedisSetConfig)
    const offered = NODE_CATEGORIES.flatMap((c: any) => c.items).some((i: any) => i.type === 'transformation' && i.subType === 'redis_set')
    expect(offered).toBe(true)
    expect(guideFor('redis_set').title).toBe('Redis set')
  })
})
