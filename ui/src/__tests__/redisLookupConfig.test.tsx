import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { RedisLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/RedisLookupConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { guideFor } from '@/lib/transformationGuide'

// The keys written here are the ones pkg/comm/transformer/lookup/redis_lookup.go
// reads: addr, username, password, db, tls, command, key, field, valuePath,
// targetField, flattenInto, defaultValue, timeout, ttl, onMiss.
describe('redis_lookup editor', () => {
  const renderConfig = (config: any, updateNodeConfig = vi.fn(), extra: Record<string, any> = {}) => {
    render(
      <MantineProvider>
        <RedisLookupConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" {...extra} />
      </MantineProvider>
    )
    return updateNodeConfig
  }

  it('writes the key under key', async () => {
    const update = renderConfig({})
    await userEvent.type(screen.getByLabelText(/^key/i), 'u')
    expect(update).toHaveBeenLastCalledWith('n1', { key: 'u' })
  })

  it('reads with GET unless told otherwise, and asks for a field only for HGET', async () => {
    renderConfig({})
    expect((screen.getByRole('combobox', { name: /^command/i }) as HTMLInputElement).value).toMatch(/GET/)
    expect(screen.queryByLabelText(/^hash field/i)).toBeNull()
  })

  it('writes the hash field under field for HGET', async () => {
    const update = renderConfig({ command: 'hget' })
    await userEvent.type(screen.getByLabelText(/^hash field/i), 's')
    expect(update).toHaveBeenLastCalledWith('n1', { field: 's' })
  })

  it('writes the chosen command', async () => {
    const update = renderConfig({})
    await userEvent.click(screen.getByRole('combobox', { name: /^command/i }))
    await userEvent.click(screen.getByText(/HGETALL/))
    expect(update).toHaveBeenCalledWith('n1', { command: 'hgetall' })
  })

  it('keeps the connection on its own tab, with the password hidden', async () => {
    const update = renderConfig({})
    await userEvent.click(screen.getByRole('tab', { name: /connection/i }))
    await userEvent.type(screen.getByLabelText(/^address/i), 'r')
    expect(update).toHaveBeenLastCalledWith('n1', { addr: 'r' })
    const pw = screen.getByLabelText(/^password/i) as HTMLInputElement
    expect(pw.type).toBe('password')
  })

  it('writes the database as a number', async () => {
    const update = renderConfig({})
    await userEvent.click(screen.getByRole('tab', { name: /connection/i }))
    await userEvent.type(screen.getByLabelText(/^database/i), '3')
    expect(update).toHaveBeenLastCalledWith('n1', { db: 3 })
  })

  it('infers the miss policy the way the backend does', async () => {
    renderConfig({ defaultValue: 'unknown' })
    await userEvent.click(screen.getByRole('tab', { name: /settings/i }))
    const policy = screen.getByRole('combobox', { name: /when the key is not found/i }) as HTMLInputElement
    expect(policy.value).toMatch(/default value/i)
  })

  it('warns that the default policy writes nothing without a default value', async () => {
    renderConfig({ onMiss: 'default' })
    await userEvent.click(screen.getByRole('tab', { name: /settings/i }))
    expect(screen.getByTestId('redis-lookup-miss-warning')).toHaveTextContent(/nothing will be written/i)
  })

  it('runs the test lookup and shows its outcome', async () => {
    const testLookup = vi.fn()
    renderConfig({}, vi.fn(), { testLookup, lastTest: { ok: false, message: 'redis_lookup: GET k found nothing' } })
    await userEvent.click(screen.getByRole('button', { name: /test redis lookup/i }))
    expect(testLookup).toHaveBeenCalled()
    expect(screen.getByTestId('redis-lookup-test-result')).toHaveTextContent(/found nothing/)
  })

  it('is registered, offered in the palette, and has a guide', () => {
    expect(TRANSFORM_CONFIGS.redis_lookup).toBe(RedisLookupConfig)
    const offered = NODE_CATEGORIES.flatMap((c: any) => c.items).some((i: any) => i.subType === 'redis_lookup')
    expect(offered).toBe(true)
    expect(guideFor('redis_lookup').title).toBe('Redis lookup')
  })
})
