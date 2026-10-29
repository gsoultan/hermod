import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it, vi } from 'vitest'
import { IconGitBranch } from '@tabler/icons-react'
import { EmptyState, FilterBar, PageHeader, Row, RowList, SectionHeader, StatePill } from '@/components/rows'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params: _p, search: _s, ...props }: any) => <a href={to} {...props}>{children}</a>,
}))

const wrap = (ui: React.ReactNode) => render(<MantineProvider>{ui}</MantineProvider>)

describe('PageHeader', () => {
  it('is the page title in the words of its sidebar link, with the actions on the right', () => {
    wrap(<PageHeader title="Flows" lede="Every flow, with its switch." actions={<button>New flow</button>} />)
    expect(screen.getByRole('heading', { level: 1, name: 'Flows' })).toBeInTheDocument()
    expect(screen.getByText('Every flow, with its switch.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New flow' })).toBeInTheDocument()
  })

  it('offers a way back when told where back is', () => {
    wrap(<PageHeader title="Orders to warehouse" back={{ to: '/workflows', label: 'Back to flows' }} />)
    expect(screen.getByRole('link', { name: 'Back to flows' })).toHaveAttribute('href', '/workflows')
  })
})

describe('Row', () => {
  it('reads icon, name, one line of detail, state, action and arrow, in that order', () => {
    wrap(
      <RowList aria-label="Flows">
        <Row
          icon={IconGitBranch}
          title="Orders to warehouse"
          detail="orders-db → snowflake"
          state={<StatePill tone="err">Failed 9 min ago</StatePill>}
          action={<button>Retry</button>}
          to="/workflows/wf1"
        />
      </RowList>,
    )
    const list = screen.getByRole('list', { name: 'Flows' })
    const item = within(list).getByRole('listitem')
    expect(within(item).getByText('Orders to warehouse')).toBeInTheDocument()
    expect(within(item).getByText('orders-db → snowflake')).toBeInTheDocument()
    expect(within(item).getByText('Failed 9 min ago')).toBeInTheDocument()
    expect(within(item).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    const link = within(item).getByRole('link', { name: /Orders to warehouse/ })
    expect(link).toHaveAttribute('href', '/workflows/wf1')
  })

  it('keeps the action out of the link, so Retry does not also open the row', async () => {
    const onRetry = vi.fn()
    wrap(
      <RowList aria-label="Flows">
        <Row title="Billing sync" action={<button onClick={onRetry}>Retry</button>} to="/workflows/wf2" />
      </RowList>,
    )
    const action = screen.getByRole('button', { name: 'Retry' })
    expect(action.closest('a')).toBeNull()
    await userEvent.click(action)
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('is a plain row, not a link, when it goes nowhere', () => {
    wrap(<RowList aria-label="People"><Row title="gembit" detail="Administrator" /></RowList>)
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText('gembit')).toBeInTheDocument()
  })
})

describe('StatePill', () => {
  it('names its tone for assistive tech and styling alike', () => {
    wrap(<StatePill tone="ok">412 /s</StatePill>)
    const pill = screen.getByText('412 /s')
    expect(pill).toHaveAttribute('data-tone', 'ok')
  })
})

describe('FilterBar', () => {
  it('is a search box and one segmented filter', async () => {
    const onSearch = vi.fn()
    const onFilter = vi.fn()
    wrap(
      <FilterBar
        search={{ value: '', onChange: onSearch, placeholder: 'Search flows' }}
        filter={{ value: 'all', onChange: onFilter, options: [
          { value: 'all', label: 'All' }, { value: 'running', label: 'Running' }, { value: 'problems', label: 'Problems' }, { value: 'off', label: 'Off' },
        ] }}
      />,
    )
    await userEvent.type(screen.getByRole('searchbox', { name: 'Search flows' }), 'o')
    expect(onSearch).toHaveBeenCalledWith('o')
    await userEvent.click(screen.getByRole('radio', { name: 'Problems' }))
    expect(onFilter).toHaveBeenCalledWith('problems')
  })
})

describe('EmptyState', () => {
  it('says what to do next with exactly one button', () => {
    const onClick = vi.fn()
    wrap(<EmptyState title="No flows yet" detail="A flow moves rows from a source to a sink." action={{ label: 'New flow', onClick }} />)
    expect(screen.getByText('No flows yet')).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(1)
  })

  it('can send the person somewhere instead', () => {
    wrap(<EmptyState title="No connections" action={{ label: 'Add connection', to: '/connections/new' }} />)
    expect(screen.getByRole('link', { name: 'Add connection' })).toHaveAttribute('href', '/connections/new')
  })
})

describe('SectionHeader', () => {
  it('heads a tab inside a page: the tab label as a second-level heading, a lede, a badge and the actions', () => {
    wrap(<SectionHeader title="Dead letters" lede="Messages a flow gave up on." meta={<span>prod</span>} actions={<button>Export CSV</button>} />)
    expect(screen.getByRole('heading', { level: 2, name: 'Dead letters' })).toBeInTheDocument()
    expect(screen.getByText('Messages a flow gave up on.')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Export CSV' })).toBeInTheDocument()
  })
})

describe('Row avatar', () => {
  it('shows the first letter in place of an icon', () => {
    wrap(<RowList aria-label="People"><Row avatar="gembit" title="gembit" /></RowList>)
    expect(screen.getByText('G')).toBeInTheDocument()
  })
})
