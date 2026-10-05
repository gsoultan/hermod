import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { SecretNamesContext, TemplateField } from '@/components/shared/TemplateField'

/**
 * Escape in "Insert variable" or "Insert function" has to stop at the list.
 *
 * Both open over a node's settings, which is a Mantine modal: it listens for
 * Escape on the window and closes unless the key's target carries
 * data-mantine-stop-propagation. The browser spec (picker_escape_e2e) presses
 * the key in the four places focus can be; these are the two things that make
 * it hold wherever that is -- the focus is moved into the list when it opens,
 * and everything in the list that can hold the focus, the list itself
 * included, carries the attribute.
 */

const STOPS = 'data-mantine-stop-propagation'

function Field() {
  const [value, setValue] = useState('')
  return (
    <MantineProvider>
      <SecretNamesContext.Provider value={['API_KEY']}>
        <TemplateField
          aria-label="Value"
          value={value}
          onChange={setValue}
          availableFields={['after.id']}
          functions="expression"
        />
      </SecretNamesContext.Provider>
    </MantineProvider>
  )
}

const open = async (name: RegExp) => {
  const user = userEvent.setup()
  render(<Field />)
  await user.click(screen.getByRole('button', { name }))
  return screen.findByRole('dialog', { hidden: true }, { timeout: 5000 })
}

describe('Insert variable', () => {
  // With the focus left on the button that opened it, Escape never reached
  // the list at all: the settings closed and took the list with them.
  it('takes the focus when it opens', async () => {
    const list = await open(/insert variable/i)
    const search = within(list).getByPlaceholderText('Search fields...')
    await waitFor(() => expect(search).toHaveFocus())
  })

  it('keeps Escape wherever the focus is in it', async () => {
    const list = await open(/insert variable/i)
    expect(within(list).getByPlaceholderText('Search fields...')).toHaveAttribute(STOPS, 'true')
    expect(within(list).getByRole('button', { name: /API_KEY/, hidden: true })).toHaveAttribute(STOPS, 'true')
  })

  // A click on the list's own text focuses the nearest thing that can take
  // it. That has to be the list, not the page behind.
  it('can hold the focus itself, and keeps Escape there too', async () => {
    const list = await open(/insert variable/i)
    expect(list).toHaveAttribute('tabindex', '-1')
    expect(list).toHaveAttribute(STOPS, 'true')
  })
})

describe('Insert function', () => {
  it('can hold the focus itself, and keeps Escape there too', async () => {
    const list = await open(/insert function/i)
    expect(list).toHaveAttribute('tabindex', '-1')
    expect(list).toHaveAttribute(STOPS, 'true')
  })
})
