import { screen, waitFor } from '@testing-library/react'
import { expect } from 'vitest'

/**
 * Finds a button and waits until it can be pressed. The Machine Learning page
 * shows its training buttons before the worker status arrives, off until it
 * says the worker is ready; clicking one before then does nothing. Queried
 * again on every try, as the page may render it anew.
 */
export async function findEnabledButton(name: string | RegExp): Promise<HTMLElement> {
  let button: HTMLElement | undefined
  await waitFor(() => {
    button = screen.getByRole('button', { name })
    expect(button).toBeEnabled()
  })
  return button as HTMLElement
}

type User = { click: (element: Element) => Promise<void> }

/**
 * Opens the training dialog the way a person does: Add model, then the "Train
 * a new model" choice once the worker status has turned it on.
 */
export async function openTraining(user: User) {
  await user.click((await screen.findAllByRole('button', { name: /^add model$/i }))[0])
  await user.click(await findEnabledButton(/train a new model/i))
}

/**
 * Opens a model's "…" menu and returns one of its items as it is: it may be
 * off, saying why. Mantine's menu stays mid-transition under jsdom, so its
 * items are found with hidden: true, as Select options are.
 */
export async function modelAction(user: User, model: string, item: string | RegExp): Promise<HTMLElement> {
  await user.click(await screen.findByRole('button', { name: `More actions for ${model}` }))
  return screen.findByRole('menuitem', { name: item, hidden: true })
}

/**
 * Opens a model's "…" menu and waits until one of its items can be pressed:
 * findEnabledButton for an item that is off until the worker status arrives.
 */
export async function findEnabledModelAction(user: User, model: string, item: string | RegExp): Promise<HTMLElement> {
  await modelAction(user, model, item)
  let found: HTMLElement | undefined
  await waitFor(() => {
    found = screen.getByRole('menuitem', { name: item, hidden: true })
    expect(found).toBeEnabled()
  })
  return found as HTMLElement
}
