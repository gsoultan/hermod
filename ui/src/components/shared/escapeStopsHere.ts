/**
 * Marks an element so that Escape pressed on it does not close the modal or
 * drawer behind it.
 *
 * Mantine's modal listens for Escape on the window, in the capture phase, and
 * closes unless the key's target carries this attribute -- nothing nearer the
 * target can stop it sooner. A list opened over a node's settings therefore
 * needs it on everything in it that can hold the focus, the list's own
 * container included: a click on plain text focuses the nearest element that
 * can take it, and Escape is then pressed there.
 *
 * The list also has to take the focus when it opens. Left on the button that
 * opened it, Escape never reaches the list at all.
 */
export const ESCAPE_STOPS_HERE = { 'data-mantine-stop-propagation': 'true' } as const
