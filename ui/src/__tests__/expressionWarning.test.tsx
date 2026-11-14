import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { FilterEditor } from '@/components/workflow/Transformation/FilterEditor'
import { expressionWarning } from '@/lib/functionCatalog'

// A condition's Field (If, Filter) and a Switch's field read a call the way the
// engine does, and two ways of writing one evaluate to null without any error:
// a function name the engine does not have, and a field written bare inside the
// call, which the engine reads as the text of its name. Either way the node
// compares null with the value, so `array_length(source.users) > 0` and
// `array_len(users) > 0` are false for every record while the canvas stays
// green. Set Fields already warned about the first; these inputs said nothing.
const FIELDS = ['users', 'name', 'profile.city']

describe('expressionWarning', () => {
  it('stays quiet for a correct call', () => {
    expect(expressionWarning('array_len(source.users)', FIELDS)).toBeUndefined()
    expect(expressionWarning('lower(source.profile.city)', FIELDS)).toBeUndefined()
    expect(expressionWarning('date_diff(source.created_at, "2024-03-01", "day")', FIELDS)).toBeUndefined()
  })

  it('stays quiet for a plain field path', () => {
    expect(expressionWarning('users', FIELDS)).toBeUndefined()
    expect(expressionWarning('after.users', FIELDS)).toBeUndefined()
    expect(expressionWarning('', FIELDS)).toBeUndefined()
  })

  it('names a function the engine does not have, and the one probably meant', () => {
    const msg = expressionWarning('array_length(source.users)', FIELDS)
    expect(msg).toMatch(/no function named array_length/i)
  })

  it('names a field written bare inside a call and shows how to read it', () => {
    const msg = expressionWarning('array_len(users)', FIELDS)
    expect(msg).toMatch(/users/)
    expect(msg).toContain('source.users')
  })

  it('finds a bare field nested in another call', () => {
    expect(expressionWarning('upper(trim(name))', FIELDS)).toContain('source.name')
    expect(expressionWarning('lower(profile.city)', FIELDS)).toContain('source.profile.city')
  })

  it('leaves a bare word that is not a field alone: it is an argument like "day"', () => {
    expect(expressionWarning('date_trunc(source.created_at, month)', FIELDS)).toBeUndefined()
  })

  it('reads template tokens too', () => {
    expect(expressionWarning('{{array_len(users)}}', FIELDS)).toContain('source.users')
    expect(expressionWarning('{{array_len(source.users)}}', FIELDS)).toBeUndefined()
  })
})


describe('the condition editor warns under the Field', () => {
  const renderWith = (field: string) =>
    render(
      <MantineProvider>
        <FilterEditor
          conditions={[{ field, operator: '>', value: '0' }]}
          availableFields={[{ path: 'users' }]}
          onChange={() => {}}
        />
      </MantineProvider>,
    )

  it('for a bare field inside a call', () => {
    renderWith('array_len(users)')
    expect(screen.getByTestId('expression-warning')).toHaveTextContent('source.users')
  })

  it('for an unknown function', () => {
    renderWith('array_length(source.users)')
    expect(screen.getByTestId('expression-warning')).toHaveTextContent(/array_length/)
  })

  it('not for a correct call', () => {
    renderWith('array_len(source.users)')
    expect(screen.queryByTestId('expression-warning')).not.toBeInTheDocument()
  })
})
