import { expect, type Page } from '@playwright/test';

/**
 * Accept the in-app confirmation dialog.
 *
 * Destructive actions confirm through ConfirmProvider — a Mantine Modal
 * rendered in the DOM — not through window.confirm. Specs that registered a
 * `page.on('dialog', ...)` handler were waiting for an event that never fires:
 * the click opened the modal, nothing accepted it, the delete silently did not
 * happen, and the assertion blamed the row for still being there.
 *
 * Two shapes have to be handled. The plain one is a message and a confirm
 * button. The guarded one (ConfirmProvider's `confirmText`) additionally
 * requires typing the resource's name, and leaves the confirm button *disabled*
 * until it matches — so clicking without typing waits forever on a control that
 * will never become enabled. The required text is the input's placeholder.
 *
 * The confirm button's label is caller-supplied ("Confirm", "Delete", "Delete
 * VHost", ...), so it is identified by elimination: the dialog's last button
 * that is not Cancel.
 */
export async function acceptConfirm(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog');
  await expect(dialog, 'confirmation dialog did not open').toBeVisible({ timeout: 10000 });

  // Guarded delete: type the exact text the modal asks for.
  const typeToConfirm = dialog.getByRole('textbox');
  if (await typeToConfirm.count()) {
    const required = await typeToConfirm.first().getAttribute('placeholder');
    if (required) {
      await typeToConfirm.first().fill(required);
    }
  }

  const confirm = dialog
    .getByRole('button')
    .filter({ hasNotText: /^cancel$/i })
    .last();
  await expect(confirm, 'confirm button never became enabled').toBeEnabled({ timeout: 10000 });
  await confirm.click();

  await expect(dialog).toBeHidden({ timeout: 10000 });
}
