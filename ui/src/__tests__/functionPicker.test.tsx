import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { TemplateField } from '@/components/shared/TemplateField'
import { FilterEditor, type Condition } from '@/components/workflow/Transformation/FilterEditor'
import HelpContent from '@/components/workflow/Transformation/HelpContent'
import { MaskConfig } from '@/components/workflow/Transformation/configs/data/MaskConfig'
import { SetFieldsConfig } from '@/components/workflow/Transformation/configs/data/SetFieldsConfig'
import catalog from '@/lib/expressionFunctions.json'
import { server, signInAs } from '../test/setupTests'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

/**
 * The engine has always run lower(), upper(), now() and thirty more in a Set
 * Fields value. The editor never said so: the only list was a card beside the
 * Formulas node, it held 13 of the 33, and picking one added a new row instead
 * of going into the value being written. A function is offered where a value
 * is typed now, from the one list the engine's own test checks.
 */

type User = ReturnType<typeof userEvent.setup>

const openFunctions = async (user: User, index = 0) => {
  await user.click(screen.getAllByRole('button', { name: /insert function/i })[index])
  return screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
}

const pick = async (user: User, picker: HTMLElement, search: string, name: RegExp) => {
  // The list is loaded when the picker first opens.
  await user.type(await within(picker).findByRole('textbox', { name: /search functions/i, hidden: true }), search)
  await user.click(within(picker).getByRole('button', { name, hidden: true }))
}

function SetFields({ initial }: { initial: Record<string, unknown> }) {
  const [config, setConfig] = useState<Record<string, unknown>>(initial)
  const updateNodeConfig = (_id: string, next: any, replace = false) =>
    setConfig((prev) => (replace ? next : { ...prev, ...next }))
  return (
    <MantineProvider>
      <SetFieldsConfig
        config={config}
        updateNodeConfig={updateNodeConfig}
        nodeId="n1"
        availableFields={['after.id', 'name']}
        addField={() => {}}
      />
      <output data-testid="config">{JSON.stringify(config)}</output>
    </MantineProvider>
  )
}

const config = () => JSON.parse(screen.getByTestId('config').textContent || '{}')
const valueBox = () => screen.findByRole('textbox', { name: /value or expression/i })

describe('picking a function in a Set Fields value', () => {
  it('applies it to the value the row already holds', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.name': 'source.name' }} />)
    await valueBox()

    await pick(user, await openFunctions(user), 'upper', /^insert upper\(/i)

    expect(config()['column.name']).toBe('upper(source.name)')
  }, 20000)

  it('fills an empty value, and the next variable picked becomes its argument', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.phone': '' }} />)
    await valueBox()

    await pick(user, await openFunctions(user), 'replace', /^insert replace\(/i)
    expect(config()['column.phone']).toBe("replace(value, 'find', 'with')")

    // The placeholder is left selected, so this replaces it.
    await waitFor(async () => expect(((await valueBox()) as HTMLTextAreaElement).selectionEnd).toBe('replace(value'.length))
    await user.click(screen.getByRole('button', { name: /insert variable/i }))
    await user.click(await screen.findByText('after.id'))

    expect(config()['column.phone']).toBe("replace(source.after.id, 'find', 'with')")
  }, 20000)

  it('finds a function by what it does, not only by its name', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.nick': '' }} />)
    await valueBox()

    const picker = await openFunctions(user)
    const search = await within(picker).findByRole('textbox', { name: /search functions/i, hidden: true })
    await user.type(search, 'default')
    expect(within(picker).getByRole('button', { name: /^insert coalesce\(/i, hidden: true })).toBeInTheDocument()
    expect(within(picker).queryByRole('button', { name: /^insert upper\(/i, hidden: true })).toBeNull()

    await user.clear(search)
    await user.type(search, 'zzz')
    expect(within(picker).getByText(/no function matches/i)).toBeInTheDocument()
  }, 20000)

  // Mantine's modal closes on any Escape whose target lacks this attribute, so
  // dismissing the list closed the node's settings behind it as well. The
  // browser spec presses the key; this is the contract that makes it hold.
  it('keeps Escape to itself', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.a': '' }} />)
    await valueBox()

    const picker = await openFunctions(user)
    const search = await within(picker).findByRole('textbox', { name: /search functions/i, hidden: true })
    expect(search).toHaveAttribute('data-mantine-stop-propagation', 'true')
    expect(within(picker).getByRole('button', { name: /^insert lower\(/i, hidden: true })).toHaveAttribute(
      'data-mantine-stop-propagation',
      'true'
    )
  }, 20000)

  it('offers every function the catalog lists, with the result of its example', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.a': '' }} />)
    await valueBox()

    const picker = await openFunctions(user)
    await within(picker).findByRole('textbox', { name: /search functions/i, hidden: true })
    for (const fn of catalog.functions) {
      const offered = within(picker).getByRole('button', { name: `Insert ${fn.signature}`, hidden: true })
      // now() is its own example; it is written once.
      expect(within(offered).getAllByText(fn.example)).toHaveLength(1)
    }
    const lower = within(picker).getByRole('button', { name: 'Insert lower(text)', hidden: true })
    expect(within(lower).getByText('"ada@example.com"')).toBeInTheDocument()
  }, 20000)
})

// A value holding {{ is read as a template, and there a bare source.x is text.
describe('a Set Fields value that is a template', () => {
  it('takes a variable as a token', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.auth': 'Bearer {{source.token}} ' }} />)
    await valueBox()

    await user.click(screen.getByRole('button', { name: /insert variable/i }))
    await user.click(await screen.findByText('after.id'))

    expect(config()['column.auth']).toBe('Bearer {{source.token}} {{source.after.id}}')
  }, 20000)

  it('takes a function as a token', async () => {
    const user = userEvent.setup()
    render(<SetFields initial={{ transType: 'set', 'column.auth': 'Bearer {{source.token}} ' }} />)
    await valueBox()

    await pick(user, await openFunctions(user), 'now', /^insert now\(/i)

    expect(config()['column.auth']).toBe('Bearer {{source.token}} {{now()}}')
  }, 20000)
})

// time.now() is written out as that text and nwo() as null, and neither is an
// error anywhere: the node is green and the column is wrong.
describe('a Set Fields value that calls no function', () => {
  it('says so, and names the function probably meant', async () => {
    render(<SetFields initial={{ transType: 'set', 'column.at': 'time.now()' }} />)
    await valueBox()
    expect(screen.getByText(/time\.now is not a function.*written as the text you see.*did you mean now\(\)\?/i)).toBeInTheDocument()
  })

  it('says a name the engine does not know gives nothing', async () => {
    render(<SetFields initial={{ transType: 'set', 'column.city': 'Paris (France)' }} />)
    await valueBox()
    expect(screen.getByText(/no function named Paris.*put it in quotes/i)).toBeInTheDocument()
  })

  it('says nothing about a value the engine evaluates', async () => {
    render(<SetFields initial={{ transType: 'set', 'column.name': 'upper(trim(source.name))' }} />)
    await valueBox()
    expect(screen.queryByText(/function named|is not a function/i)).toBeNull()
  })
})

// The Mask node reads its field as a path (mask.go: GetMsgValByPath). Its
// input said "Field or expression" and suggested lower(source.email), which
// names no field, so the node passed every record through unmasked.
describe('a field the engine reads as a path', () => {
  it('neither offers functions nor suggests one', () => {
    render(
      <MantineProvider>
        <MaskConfig config={{ transType: 'mask' }} updateNodeConfig={() => {}} nodeId="n1" availableFields={['user.email']} />
      </MantineProvider>
    )
    const field = screen.getByRole('combobox', { name: /field path/i })
    expect(field.getAttribute('placeholder')).not.toMatch(/\(source\./)
    expect(screen.queryByText(/or expression/i)).toBeNull()
    expect(screen.queryByRole('button', { name: /insert function/i })).toBeNull()
  })
})

describe('a template field', () => {
  function Field({ functions, initial = '' }: { functions?: 'token'; initial?: string }) {
    const [value, setValue] = useState(initial)
    return (
      <MantineProvider>
        <TemplateField
          aria-label="Body"
          value={value}
          onChange={setValue}
          availableFields={['after.id']}
          functions={functions}
        />
      </MantineProvider>
    )
  }

  // Not every templated field is resolved by something that runs a call, so
  // the picker is something a field asks for.
  it('offers no functions unless it says how they are written', () => {
    render(<Field />)
    expect(screen.queryByRole('button', { name: /insert function/i })).toBeNull()
  })

  // source.x is how a call's argument names a field. Outside the fields that
  // offer functions nothing says the resolver reads it, so a field that does
  // not offer them inserts what it always has.
  it('inserts a variable as its own token when it offers no functions', async () => {
    const user = userEvent.setup()
    render(<Field initial="{{" />)
    const box = screen.getByRole('textbox', { name: 'Body' })
    await user.click(box)

    await user.click(screen.getByRole('button', { name: /insert variable/i }))
    await user.click(await screen.findByText('after.id'))

    expect(box).toHaveValue('{{{{.after.id}}')
  }, 20000)

  it('writes a call as a token, and a variable inside it as source.path', async () => {
    const user = userEvent.setup()
    render(<Field functions="token" initial="Hello " />)

    await pick(user, await openFunctions(user), 'upper', /^insert upper\(/i)
    const box = screen.getByRole('textbox', { name: 'Body' }) as HTMLInputElement
    expect(box).toHaveValue('Hello {{upper(value)}}')

    await waitFor(() => expect(box.selectionEnd).toBe('Hello {{upper(value'.length))
    await user.click(screen.getByRole('button', { name: /insert variable/i }))
    await user.click(await screen.findByText('after.id'))

    // {{upper({{.after.id}})}} is what the variable picker wrote on its own.
    expect(box).toHaveValue('Hello {{upper(source.after.id)}}')
  }, 20000)
})

describe('a condition', () => {
  function Conditions({ initial }: { initial: Condition[] }) {
    const [conditions, setConditions] = useState(initial)
    return (
      <MantineProvider>
        <FilterEditor conditions={conditions} availableFields={['status']} onChange={setConditions} />
        <output data-testid="conditions">{JSON.stringify(conditions)}</output>
      </MantineProvider>
    )
  }
  const conditions = () => JSON.parse(screen.getByTestId('conditions').textContent || '[]')

  // A bare `status` is a field; inside a call it would be the text "status".
  it('applies a function to its field, written as the engine reads one in a call', async () => {
    const user = userEvent.setup()
    render(<Conditions initial={[{ field: 'status', operator: '=', value: 'paid' }]} />)

    await pick(user, await openFunctions(user, 0), 'lower', /^insert lower\(/i)

    expect(conditions()[0].field).toBe('lower(source.status)')
  }, 20000)

  it('writes a function in its value as a token', async () => {
    const user = userEvent.setup()
    render(<Conditions initial={[{ field: 'created', operator: '<', value: '' }]} />)

    await pick(user, await openFunctions(user, 1), 'now', /^insert now\(/i)

    expect(conditions()[0].value).toBe('{{now()}}')
  }, 20000)
})

describe('the function list beside the editor', () => {
  const renderForm = (transType: string, updateNodeConfig = vi.fn()) => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={qc}>
          <VHostProvider>
            <TransformationForm
              selectedNode={{ id: 'n1', type: 'transformation', data: { transType } } as any}
              updateNodeConfig={updateNodeConfig}
              availableFields={[]}
              incomingPayload={{ full_name: 'Ada King Lovelace' }}
              sinkSchema={{}}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
    return updateNodeConfig
  }

  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
  })

  // It was rendered for Formulas only, though Set Fields reads the same values.
  it('is shown for Set Fields, with functions the old list never had', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderForm('set')

    const library = await screen.findByRole('region', { name: /function library/i }, { timeout: 5000 })
    const offered = await within(library).findByRole('button', { name: 'Insert uuid()' }, { timeout: 5000 })
    await user.click(offered)

    expect(updateNodeConfig).toHaveBeenCalledWith('n1', { 'column.new_field_0': 'uuid()' })
  }, 20000)

  it('is not shown for a node whose values are not expressions', async () => {
    renderForm('mask')
    await screen.findByText(/TRANSFORM LOGIC/i)
    expect(screen.queryByRole('region', { name: /function library/i })).toBeNull()
  }, 20000)
})

describe('the help', () => {
  it('lists every function, grouped as the picker groups them', async () => {
    render(
      <MantineProvider>
        <HelpContent />
      </MantineProvider>
    )
    for (const category of catalog.categories) {
      expect(await screen.findByRole('heading', { name: category })).toBeInTheDocument()
    }
    for (const fn of catalog.functions) {
      expect(screen.getByText(fn.signature)).toBeInTheDocument()
    }
  })
})
