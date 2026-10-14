import { useMemo, useState } from 'react'
import { Alert, Button, Checkbox, Code, Group, Modal, Radio, Stack, Table, Text, Textarea } from '@mantine/core'
import { IconAlertTriangle, IconCheck } from '@tabler/icons-react'
import { apiFetch } from '@/api'
import { checkEdit, describeValue, prettyJSON, type DeadLetter, type ReplayOutcome, type ReplayStep } from '@/lib/deadLetters'

interface Props {
  deadLetter: DeadLetter
  payload: string
  failedAt: string
  onClose: () => void
  onReplayed: (outcome: ReplayOutcome) => void
}

// ReplayDeadLetterModal edits a dead letter's payload and sends it again, from
// the node that failed it or from the start, optionally showing first what
// each node would make of it.
export function ReplayDeadLetterModal({ deadLetter, payload, failedAt, onClose, onReplayed }: Props) {
  const original = useMemo(() => prettyJSON(payload), [payload])
  const [text, setText] = useState(deadLetter.truncated ? '' : original)
  const [fromStart, setFromStart] = useState<'node' | 'start'>('node')
  const [testFirst, setTestFirst] = useState(true)
  const [steps, setSteps] = useState<ReplayStep[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const check = useMemo(() => checkEdit(original, text), [original, text])
  const edited = text !== original

  const body = (test: boolean) => JSON.stringify({
    ...(edited || deadLetter.truncated ? { payload: compact(text) } : {}),
    from_start: fromStart === 'start',
    test,
  })

  const send = async (test: boolean) => {
    setBusy(true)
    setError(null)
    try {
      const res = await apiFetch(`/api/dead-letters/${encodeURIComponent(deadLetter.id)}/replay`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: body(test),
        silent: true,
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        setError(data.error || 'The replay was refused')
        return
      }
      if (test) {
        setSteps(data.steps || [])
        return
      }
      onReplayed(data as ReplayOutcome)
    } finally {
      setBusy(false)
    }
  }

  const stepFailed = steps?.some((s) => s.error)
  const changed = check.changes

  return (
    <Modal opened onClose={onClose} size="xl" title={`Edit and replay ${deadLetter.message_id}`}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          The edited message goes back into {deadLetter.workflow_name || deadLetter.workflow_id} at the node that rejected it.
          The original stays in the queue until the replay is delivered.
        </Text>
        <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />}>
          Failed at {failedAt}: {deadLetter.error}
        </Alert>
        {deadLetter.truncated && (
          <Alert color="yellow" variant="light">
            The original payload was too large to keep whole, so it cannot be replayed as it was. Paste the full message to replay it.
          </Alert>
        )}
        <Textarea
          label="Payload"
          value={text}
          onChange={(e) => { setText(e.currentTarget.value); setSteps(null) }}
          autosize
          minRows={10}
          maxRows={18}
          spellCheck={false}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 13 } }}
        />
        {!check.valid ? (
          <Text size="sm" c="red" aria-live="polite">This is not valid JSON yet.</Text>
        ) : (
          <Group gap={6} aria-live="polite">
            <IconCheck size={16} color="var(--mantine-color-green-7)" />
            <Text size="sm" c="green.8">
              Valid JSON · {changed.length === 0 ? 'no fields changed' : `${changed.length} field${changed.length === 1 ? '' : 's'} changed`}
              {changed.slice(0, 3).map((c) => `: ${c.path} ${describeValue(c.before)} → ${describeValue(c.after)}`).join(';')}
              {changed.length > 3 ? ` and ${changed.length - 3} more` : ''}
            </Text>
          </Group>
        )}

        <Radio.Group label="Start from" value={fromStart} onChange={(v) => { setFromStart(v as 'node' | 'start'); setSteps(null) }}>
          <Stack gap={6} mt={6}>
            <Radio value="node" label={`The node that rejected it (${failedAt})`} />
            <Radio value="start" label="The start of the workflow" />
          </Stack>
        </Radio.Group>
        <Checkbox
          label="Test against the workflow first and show what each node would produce"
          checked={testFirst}
          onChange={(e) => { setTestFirst(e.currentTarget.checked); setSteps(null) }}
        />

        {steps && (
          <Stack gap={6}>
            <Text size="sm" fw={600}>{stepFailed ? 'The test failed at a node, so nothing was replayed.' : 'The test passed. Nothing has been sent yet.'}</Text>
            <Table withTableBorder striped>
              <Table.Thead>
                <Table.Tr><Table.Th>Node</Table.Th><Table.Th>Result</Table.Th></Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {steps.map((s, i) => (
                  <Table.Tr key={`${s.node_id}-${i}`}>
                    <Table.Td>{s.node_id}</Table.Td>
                    <Table.Td>
                      {s.error ? <Text size="sm" c="red">{s.error}</Text>
                        : s.skipped || s.filtered ? <Text size="sm" c="dimmed">{s.skipped ? 'Not reached' : 'Filtered out'}</Text>
                          : <Code block>{JSON.stringify(s.payload ?? {}, null, 2)}</Code>}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        )}
        {error && <Alert color="red" variant="light">{error}</Alert>}

        <Group justify="space-between">
          <Button variant="subtle" onClick={() => { setText(original); setSteps(null) }} disabled={deadLetter.truncated}>
            Reset to original
          </Button>
          <Group gap="xs">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            {steps && !stepFailed ? (
              <Button onClick={() => send(false)} loading={busy}>Replay now</Button>
            ) : (
              <Button onClick={() => send(testFirst)} loading={busy} disabled={!check.valid || !text.trim()}>
                {testFirst ? 'Test and replay' : 'Replay'}
              </Button>
            )}
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}

// compact sends the edit as the user's JSON without the indentation.
function compact(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text))
  } catch {
    return text
  }
}
