import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'
import { NotificationSinkConfig } from '@/components/workflow/Sink/NotificationSinkConfig'

// Discord and Slack are offered in the sink picker and both map to this
// component, which had a case for Telegram and `default: return null` — so
// picking either one showed an empty form. The wizard has no required keys for
// them either, so the sink saved happily and failed on the first message with
// "not configured: missing webhook_url or token/channel_id".
describe('chat sink forms', () => {
  const setup = (type: string) =>
    render(
      <MantineProvider>
        <NotificationSinkConfig type={type} config={{}} updateConfig={() => {}} />
      </MantineProvider>
    )

  it.each([
    ['telegram', [/bot token/i, /chat id/i]],
    ['discord', [/webhook url/i, /bot token/i, /channel id/i]],
    ['slack', [/webhook url/i, /bot token/i, /channel id/i]],
  ] as const)('%s asks for what its sink reads', (type, labels) => {
    setup(type)
    for (const label of labels) {
      expect(screen.getByLabelText(label)).toBeTruthy()
    }
  })

  // Derived from the routing map rather than listed here, so a fourth type
  // pointed at this form fails until it has a branch, instead of rendering
  // nothing and saving an unconfigured sink.
  const routedHere = SINK_TYPES
    .map((t) => t.value)
    .filter((value) => configComponents[value] === configComponents['telegram'])

  it('routes exactly the chat types here', () => {
    expect(routedHere.sort()).toEqual(['discord', 'slack', 'telegram'])
  })

  it.each(routedHere)('%s renders fields rather than nothing', (type) => {
    setup(type)
    expect(screen.getAllByRole('textbox').length).toBeGreaterThan(0)
  })
})
