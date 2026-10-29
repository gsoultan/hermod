import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { signInAs } from '../test/setupTests'

const navigate = vi.fn()
let search: Record<string, unknown> = {}
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useSearch: () => search,
}))
vi.mock('../pages/sources/SourcesPage', () => ({ SourcesPage: () => <div>source rows</div> }))
vi.mock('../pages/sinks/SinksPage', () => ({ SinksPage: () => <div>sink rows</div> }))
vi.mock('../pages/Marketplace/CommunityMarketplace', () => ({ CommunityMarketplace: () => <div>community</div> }))

import { ConnectionsPage } from '@/pages/hubs/ConnectionsPage'

const renderPage = () => render(<MantineProvider><ConnectionsPage /></MantineProvider>)

beforeEach(() => { navigate.mockReset(); search = {}; signInAs('Editor') })

describe('Connections', () => {
  it('puts the one Add button for the open tab beside the title', async () => {
    renderPage()
    expect(await screen.findByText('source rows')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Add source' }))
    expect(navigate).toHaveBeenCalledWith({ to: '/sources/new' })
  })

  it('says Add sink on the Sinks tab and nothing on Community', async () => {
    search = { tab: 'sinks' }
    const { unmount } = renderPage()
    expect(await screen.findByText('sink rows')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add sink' })).toBeInTheDocument()
    unmount()
    search = { tab: 'community' }
    renderPage()
    expect(await screen.findByText('community')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Add/ })).not.toBeInTheDocument()
  })

  it('gives a Viewer no Add button', async () => {
    signInAs('Viewer')
    renderPage()
    await screen.findByText('source rows')
    expect(screen.queryByRole('button', { name: 'Add source' })).not.toBeInTheDocument()
  })
})
