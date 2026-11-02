import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { MetisDecideConfig } from '@/components/workflow/Transformation/configs/enrichment/MetisDecideConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { guideFor } from '@/lib/transformationGuide'
import { transformationLabel } from '@/pages/workflows/WorkflowEditor/nodes/transformationLabel'

// The keys written here are the ones pkg/comm/transformer/lookup/metis_decide.go
// reads: url, token, username, password, organizationId, projectId,
// decisionKey, version, inputs [{name, field}], targetField, mergeOutputs,
// onNoMatch, timeout.
describe('metis_decide editor', () => {
  const renderConfig = (config: any, updateNodeConfig = vi.fn(), extra: Record<string, unknown> = {}) => {
    render(
      <MantineProvider>
        <MetisDecideConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" {...extra} />
      </MantineProvider>,
    )
    return updateNodeConfig
  }

  it('writes the server address under url', async () => {
    const update = renderConfig({})
    await userEvent.type(screen.getByLabelText(/server address/i), 'h')
    expect(update).toHaveBeenLastCalledWith('n1', { url: 'h' })
  })

  it('hides the token and writes it under token', async () => {
    const update = renderConfig({})
    const input = screen.getByLabelText(/^token/i) as HTMLInputElement
    expect(input.type).toBe('password')
    await userEvent.type(input, 'k')
    expect(update).toHaveBeenLastCalledWith('n1', { token: 'k' })
  })

  it('switching to username and password clears the token', async () => {
    const update = renderConfig({ token: 'x' })
    await userEvent.click(screen.getByText(/username and password/i))
    expect(update).toHaveBeenLastCalledWith('n1', { token: '' })
    expect(screen.getByRole('textbox', { name: /^username/i })).toBeInTheDocument()
  })

  it('opens on username and password when that is what was saved', () => {
    renderConfig({ username: 'svc', password: '{{secret.P}}' })
    expect(screen.queryByLabelText(/^token/i)).not.toBeInTheDocument()
  })

  it('writes the project and decision key', async () => {
    const update = renderConfig({})
    await userEvent.type(screen.getByLabelText(/project id/i), 'p')
    expect(update).toHaveBeenLastCalledWith('n1', { projectId: 'p' })
    await userEvent.type(screen.getByLabelText(/decision key/i), 'd')
    expect(update).toHaveBeenLastCalledWith('n1', { decisionKey: 'd' })
  })

  it('refuses a decision key that reads the record', () => {
    renderConfig({ decisionKey: '{{.table}}' })
    expect(screen.getByText(/a fixed key; record fields are not read/i)).toBeInTheDocument()
  })

  it('opens on chosen fields when the palette seeded a row', () => {
    renderConfig({ inputs: [{ name: '', field: '' }] })
    expect(screen.getByLabelText('Record field 1')).toBeInTheDocument()
  })

  it('flags a version that is not a number', () => {
    renderConfig({ version: 'latest' })
    expect(screen.getByText(/a version number, or empty/i)).toBeInTheDocument()
  })

  it('maps chosen fields to the table inputs', async () => {
    const update = renderConfig({ inputs: [{ name: 'score', field: '' }] })
    await userEvent.type(screen.getByLabelText('Record field 1'), 'c')
    expect(update).toHaveBeenLastCalledWith('n1', { inputs: [{ name: 'score', field: 'c' }] })
  })

  it('sending the whole record clears the mapping', async () => {
    const update = renderConfig({ inputs: [{ name: 'score', field: 'customer.score' }] })
    await userEvent.click(screen.getByText(/the whole record/i))
    expect(update).toHaveBeenLastCalledWith('n1', { inputs: [] })
  })

  it('shows the default target field and runs the test', async () => {
    const testLookup = vi.fn()
    renderConfig({}, vi.fn(), { testLookup })
    expect((screen.getByLabelText(/target field/i) as HTMLInputElement).placeholder).toBe('decision')
    await userEvent.click(screen.getByRole('button', { name: /test decision/i }))
    expect(testLookup).toHaveBeenCalled()
  })

  it('is registered, offered in the palette with its target field, labelled, and has a guide', () => {
    expect(TRANSFORM_CONFIGS.metis_decide).toBe(MetisDecideConfig)
    const item = NODE_CATEGORIES.flatMap((c: any) => c.items).find((i: any) => i.subType === 'metis_decide')
    expect(item).toBeDefined()
    // The editor's Test reports the target field, so a new node starts with it set.
    expect(item?.defaults).toEqual({ targetField: 'decision', inputs: [{ name: '', field: '' }] })
    expect(transformationLabel('metis_decide')).toBe('Metis Decision')
    expect(guideFor('metis_decide').what).not.toBe('')
  })
})
