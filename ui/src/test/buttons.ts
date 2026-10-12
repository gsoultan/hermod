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
