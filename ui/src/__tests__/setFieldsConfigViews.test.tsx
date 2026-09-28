import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { SetFieldsConfig } from '@/components/workflow/Transformation/configs/data/SetFieldsConfig'

/**
 * The Set Fields panel edits one thing, the node's `column.*` keys, and used to
 * show it twice at once: the field rows, and under them a raw-JSON box that was
 * two lines tall because its minRows only applies to an autosized textarea. It
 * is one editor at a time now, each with the room to be read.
 */

function Harness({ initial }: { initial: Record<string, unknown> }) {
  const [config, setConfig] = useState<Record<string, unknown>>(initial)
  // Mirrors useWorkflowStore.updateNodeConfig: merge by default, replace on flag.
  const updateNodeConfig = (_id: string, next: any, replace = false) =>
    setConfig((prev) => (replace ? next : { ...prev, ...next }))
  return (
    <MantineProvider>
      <SetFieldsConfig
        config={config}
        updateNodeConfig={updateNodeConfig}
        nodeId="n1"
        availableFields={[]}
        addField={() => {}}
      />
      <output data-testid="config">{JSON.stringify(config)}</output>
    </MantineProvider>
  )
}

const editorView = () => screen.getByRole('radiogroup', { name: /editor view/i })

describe('Set Fields panel', () => {
  it('shows the field rows or the JSON, one at a time', async () => {
    render(<Harness initial={{ transType: 'set', 'column.a': 'source.b' }} />)

    expect(await screen.findByRole('combobox', { name: /target path/i })).toHaveValue('a')
    expect(screen.queryByLabelText('Fields (JSON)')).toBeNull()

    fireEvent.click(within(editorView()).getByRole('radio', { name: 'JSON' }))

    const json = await screen.findByLabelText('Fields (JSON)')
    expect(JSON.parse((json as HTMLTextAreaElement).value)).toEqual({ 'column.a': 'source.b' })
    expect(screen.queryByRole('combobox', { name: /target path/i })).toBeNull()
  })

  it('keeps an edit made in the JSON when going back to the rows', async () => {
    const user = userEvent.setup()
    render(<Harness initial={{ transType: 'set', 'column.a': 'source.b' }} />)
    fireEvent.click(within(await screen.findByRole('radiogroup', { name: /editor view/i })).getByRole('radio', { name: 'JSON' }))

    const json = await screen.findByLabelText('Fields (JSON)')
    fireEvent.change(json, {
      target: { value: '{"column.after.QueryParams": {"session": "source.after.session.sessions.0.access_token"}}' },
    })
    await waitFor(() =>
      expect(JSON.parse(screen.getByTestId('config').textContent || '{}')).toEqual({
        transType: 'set',
        'column.after.QueryParams': { session: 'source.after.session.sessions.0.access_token' },
      }),
    )

    await user.click(within(editorView()).getByRole('radio', { name: 'Visual' }))

    expect(await screen.findByRole('combobox', { name: /target path/i })).toHaveValue('after.QueryParams')
    const value = screen.getByRole('textbox', { name: /json value/i })
    expect(JSON.parse((value as HTMLTextAreaElement).value)).toEqual({
      session: 'source.after.session.sessions.0.access_token',
    })
  })
})
