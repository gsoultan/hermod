import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it, vi } from 'vitest'
import { AddStepPicker } from '@/pages/workflows/WorkflowEditor/picker/AddStepPicker'

const wrap = (ui: React.ReactNode) => render(<MantineProvider>{ui}</MantineProvider>)
const base = { opened: true, onClose: vi.fn(), onOpenPalette: vi.fn(), sources: [], sinks: [], plugins: [] }

describe('the add-a-step picker', () => {
  it('asks what should happen next and offers the seven choices', async () => {
    wrap(<AddStepPicker {...base} hasSource onPick={vi.fn()} />)
    expect(await screen.findByRole('dialog', { name: 'What should happen next?' })).toBeInTheDocument()
    const choices = screen.getAllByRole('button', { name: /^(Read from somewhere|Only keep some records|Change the data|Look something up|Branch, wait or approve|Run code or SQL|Ask AI|Send somewhere)/ })
    expect(choices).toHaveLength(8)
  })

  it('opens a choice to its node types and picks one', async () => {
    const onPick = vi.fn()
    wrap(<AddStepPicker {...base} hasSource onPick={onPick} />)
    await userEvent.click(await screen.findByRole('button', { name: /^Send somewhere/ }))
    await userEvent.click(await screen.findByRole('button', { name: /^Slack/ }))
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ type: 'sink', label: 'Slack' }))
  })

  it('searches every type by name, so typing beats browsing', async () => {
    const onPick = vi.fn()
    wrap(<AddStepPicker {...base} hasSource onPick={onPick} />)
    await userEvent.type(await screen.findByRole('searchbox', { name: 'Search steps' }), 'kafka')
    const results = screen.getByRole('list', { name: 'Matching steps' })
    expect(within(results).getAllByRole('button').map((b) => b.textContent)).toEqual(expect.arrayContaining([expect.stringMatching(/Kafka/)]))
    await userEvent.click(within(results).getAllByRole('button', { name: /^Kafka/ })[0])
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ label: 'Kafka' }))
  })

  it('offers only a source until the flow has one', async () => {
    wrap(<AddStepPicker {...base} hasSource={false} onPick={vi.fn()} />)
    expect(await screen.findByRole('button', { name: /^Read from somewhere/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /^Send somewhere/ })).toBeDisabled()
    expect(screen.getAllByText('Add a source first').length).toBeGreaterThan(0)
  })

  it('has a door to the full palette', async () => {
    const onOpenPalette = vi.fn()
    wrap(<AddStepPicker {...base} hasSource onPick={vi.fn()} onOpenPalette={onOpenPalette} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Browse the full palette' }))
    expect(onOpenPalette).toHaveBeenCalled()
  })
})
