import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { ConnectivityTab } from '@/pages/system/settings/ConnectivityTab'

function renderTab(overrides: Record<string, any> = {}) {
  const setNotifSettings = vi.fn()
  const notifSettings = {
    smtp_host: '', smtp_port: 587, smtp_user: '', smtp_password: '', smtp_from: '', smtp_ssl: false,
    default_email: '', telegram_token: '', telegram_chat_id: '', slack_webhook: '', discord_webhook: '',
    webhook_url: '', base_url: '', logs_retention_days: 30,
    panmail_url: '', panmail_api_key: '', panmail_provider_id: '', panmail_from: '', panmail_to: '',
    panmail_dead_letters: false, panmail_hide_payload: false,
    email_dead_letters: false, telegram_dead_letters: false, dead_letter_hide_payload: false,
    ...overrides,
  }
  const ctx: any = {
    notifSettings,
    setNotifSettings,
    saveNotifMutation: { mutate: vi.fn(), isPending: false },
    testNotifMutation: { mutate: vi.fn(), isPending: false },
  }
  render(<MantineProvider><ConnectivityTab ctx={ctx} /></MantineProvider>)
  return { setNotifSettings, notifSettings }
}

describe('Panmail notification settings', () => {
  it('edits the gateway and its key, which may be references', () => {
    const { setNotifSettings, notifSettings } = renderTab()
    fireEvent.change(screen.getByLabelText('Gateway URL'), { target: { value: '{{secret.PANMAIL_GATEWAYURL}}' } })
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, panmail_url: '{{secret.PANMAIL_GATEWAYURL}}' })
    fireEvent.change(screen.getByLabelText('API key'), { target: { value: '{{secret.PANMAIL_API_KEY}}' } })
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, panmail_api_key: '{{secret.PANMAIL_API_KEY}}' })
  })

  it('turns dead-letter mail on', () => {
    const { setNotifSettings, notifSettings } = renderTab()
    fireEvent.click(screen.getByRole('checkbox', { name: /^mail every dead-lettered message/i }))
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, panmail_dead_letters: true })
  })

  it('offers to leave message contents out only once dead-letter mail is on', () => {
    renderTab()
    expect(screen.queryByRole('checkbox', { name: /leave message contents out/i })).toBeNull()
  })

  it('leaves message contents out on request', () => {
    const { setNotifSettings, notifSettings } = renderTab({ panmail_dead_letters: true })
    fireEvent.click(screen.getByRole('checkbox', { name: /leave message contents out/i }))
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, panmail_hide_payload: true })
  })
})

describe('Dead-letter alerts by SMTP email and Telegram', () => {
  it('mails every dead-lettered message through SMTP on request', () => {
    const { setNotifSettings, notifSettings } = renderTab()
    fireEvent.click(screen.getByRole('checkbox', { name: /email every dead-lettered message/i }))
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, email_dead_letters: true })
  })

  it('sends every dead-lettered message to Telegram on request', () => {
    const { setNotifSettings, notifSettings } = renderTab()
    fireEvent.click(screen.getByRole('checkbox', { name: /send every dead-lettered message to telegram/i }))
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, telegram_dead_letters: true })
  })

  it('leaves message contents out of the email and Telegram alerts on request', () => {
    const { setNotifSettings, notifSettings } = renderTab({ telegram_dead_letters: true })
    fireEvent.click(screen.getByRole('checkbox', { name: /leave message contents out of email and telegram/i }))
    expect(setNotifSettings).toHaveBeenLastCalledWith({ ...notifSettings, dead_letter_hide_payload: true })
  })

  it('offers that only once one of them is on', () => {
    renderTab()
    expect(screen.queryByRole('checkbox', { name: /leave message contents out of email and telegram/i })).toBeNull()
  })
})
