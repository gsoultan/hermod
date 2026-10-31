import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { TextInput } from '@mantine/core'
import { FormRow } from '@/components/common/FormRow'

const ui = (node: React.ReactNode) => render(<MantineProvider>{node}</MantineProvider>)

describe('FormRow', () => {
  it('renders its fields', () => {
    ui(
      <FormRow>
        <TextInput label="Host" />
        <TextInput label="Port" />
      </FormRow>
    )
    expect(screen.getByLabelText('Host')).toBeInTheDocument()
    expect(screen.getByLabelText('Port')).toBeInTheDocument()
  })

  it('collapses to a single column on mobile', () => {
    const { container } = ui(
      <FormRow>
        <TextInput label="Host" />
        <TextInput label="Port" />
      </FormRow>
    )
    const grid = container.querySelector('.hermod-form-row') as HTMLElement
    expect(grid).toBeTruthy()

    // The guarantee is structural: a CSS grid, not a flex row. Mantine emits the
    // responsive column counts into an injected stylesheet rather than inline
    // vars, so the assertion is on the primitive being used. `<Group grow>` is
    // `mantine-Group-root`, a flex row that never wraps — which is exactly why
    // connection fields were squeezed to ~80px each on a phone instead of
    // stacking. SimpleGrid's `base: 1` collapses them.
    expect(grid.className).toContain('mantine-SimpleGrid-root')
    expect(grid.className).not.toContain('mantine-Group-root')

    // The breakpoint values themselves live in Mantine's own stylesheet, which
    // jsdom does not load, so there is nothing here to assert about them
    // honestly. The primitive is the contract; `cols={{ base: 1, ... }}` is
    // asserted by review, not by a test that cannot see it.
  })

  it('sizes its columns to the space it is given, not to the screen', () => {
    // A node's settings live in a drawer column a few hundred pixels wide on a
    // 1440px screen. Viewport breakpoints saw the 1440 and split that column
    // into two or three fields of ~140px each. Container queries measure the
    // row's own parent, so the same row stacks inside the drawer and still
    // sits side by side on a full-width page.
    const { container } = ui(
      <FormRow cols={3}>
        <TextInput label="A" />
        <TextInput label="B" />
        <TextInput label="C" />
      </FormRow>
    )
    const grid = container.querySelector('.hermod-form-row') as HTMLElement
    expect(grid.parentElement?.className).toContain('mantine-SimpleGrid-container')
  })

  it('bottom-aligns fields so inputs line up under uneven descriptions', () => {
    const { container } = ui(
      <FormRow>
        <TextInput label="Host" description="short" />
        <TextInput label="Port" description="a considerably longer description that wraps" />
      </FormRow>
    )
    const grid = container.querySelector('.hermod-form-row') as HTMLElement
    expect(grid.style.alignItems).toBe('end')
  })

  it('cols={1} renders fields without a grid wrapper', () => {
    const { container } = ui(
      <FormRow cols={1}>
        <TextInput label="Connection string" />
      </FormRow>
    )
    expect(container.querySelector('.hermod-form-row')).toBeNull()
    expect(screen.getByLabelText('Connection string')).toBeInTheDocument()
  })
})

/**
 * Guard, not a style preference.
 *
 * `<Group grow>` is a flex row that never wraps. Used for form fields it does
 * not collapse on a narrow viewport — it squeezes every field in the row. The
 * entity forms have been migrated to FormRow; this keeps them migrated.
 */
describe('entity form field layout', () => {
  // Vite's glob import rather than node:fs — it is typed, runs in the same
  // module graph the app uses, and needs no node types in the UI tsconfig.
  const sources = import.meta.glob(
    [
      '../components/workflow/Source/**/*.tsx',
      '../components/workflow/Sink/**/*.tsx',
      '../components/forms/**/*.tsx',
      // Transformation settings render in the same node drawer as sources and
      // sinks, in a narrower column, so a squeezed row hurts there most.
      '../components/workflow/Transformation/**/*.tsx',
    ],
    { query: '?raw', import: 'default', eager: true }
  ) as Record<string, string>

  it('reads the component sources it is guarding', () => {
    // A glob that silently matched nothing would make the guard below vacuous.
    expect(Object.keys(sources).length).toBeGreaterThan(30)
  })

  it('uses no <Group grow> in source, sink, transformation or entity form components', () => {
    const offenders = Object.entries(sources)
      .filter(([, code]) => /<Group[^>]*\sgrow\b/.test(code))
      .map(([file]) => file)

    expect(offenders).toEqual([])
  })

  it('lays out node settings by the drawer width, not the screen width', () => {
    // A media-query grid inside the node drawer reads the screen, so on a
    // laptop it splits a ~400px column as if it had a monitor's width. Node
    // settings use FormRow, or a grid with type="container".
    const nodeSettings = Object.entries(sources).filter(([file]) =>
      /\/workflow\/(Source|Sink|Transformation)\//.test(file)
    )
    expect(nodeSettings.length).toBeGreaterThan(30)

    const offenders = nodeSettings
      .filter(([, code]) =>
        [...code.matchAll(/<(SimpleGrid|Grid)\b[^>]*>/g)].some(([tag]) => !/type="container"/.test(tag))
      )
      .map(([file]) => file)

    expect(offenders).toEqual([])
  })
})
