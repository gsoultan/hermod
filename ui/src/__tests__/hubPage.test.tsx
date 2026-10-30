import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it, vi } from 'vitest'
import { HubPage } from '@/components/layout/HubPage'

const tabs = [
  { value: 'logs', label: 'Logs' },
  { value: 'dead-letters', label: 'Dead letters' },
]

describe('a hub page', () => {
  it('names the place once, lists its tabs and reports the one picked', async () => {
    const onChange = vi.fn()
    render(
      <MantineProvider>
        <HubPage title="Activity" tabs={tabs} value="logs" onChange={onChange}>
          <div>log rows</div>
        </HubPage>
      </MantineProvider>,
    )
    expect(screen.getByRole('heading', { level: 1, name: 'Activity' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual(['Logs', 'Dead letters'])
    expect(screen.getByRole('tab', { name: 'Logs' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('log rows')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'Dead letters' }))
    expect(onChange).toHaveBeenCalledWith('dead-letters')
  })

  it('falls back to the first tab when the URL names one that does not exist', () => {
    render(
      <MantineProvider>
        <HubPage title="Activity" tabs={tabs} value="nope" onChange={() => {}}>
          <div />
        </HubPage>
      </MantineProvider>,
    )
    expect(screen.getByRole('tab', { name: 'Logs' })).toHaveAttribute('aria-selected', 'true')
  })
})
