import { useEffect, useState } from 'react'
import { Alert, Button, Group, Loader, Modal, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconCheck, IconX } from '@tabler/icons-react'
import { apiFetch } from '@/api'
import { attempts, whenLabel, type CheckResult, type DeadLetter } from '@/lib/deadLetters'

interface Props {
  // ids is every record to replay; records are the ones the page knows,
  // which may be fewer when the selection spans pages.
  ids: string[]
  records: DeadLetter[]
  busy: boolean
  onClose: () => void
  onConfirm: (ids: string[]) => void
}

type SinkCheck = { id: string; sinkID: string; result?: CheckResult }

// ReplayConfirmModal says what a replay sends, in messages and in the failed
// attempts they cover, and asks each sink the records failed at whether it
// answers now, so a replay is not sent into the same refusal.
export function ReplayConfirmModal({ ids, records, busy, onClose, onConfirm }: Props) {
  const n = ids.length
  const known = records.filter((r) => ids.includes(r.id))
  const covered = known.reduce((sum, r) => sum + attempts(r), 0) + (n - known.length)
  const resent = known.filter((r) => attempts(r) > 1)
  const [checks, setChecks] = useState<SinkCheck[]>(() => {
    const seen = new Set<string>()
    const out: SinkCheck[] = []
    for (const r of known) {
      if (r.sink_id && !seen.has(r.sink_id)) {
        seen.add(r.sink_id)
        out.push({ id: r.id, sinkID: r.sink_id })
      }
    }
    return out
  })

  useEffect(() => {
    let cancelled = false
    for (const c of checks) {
      if (c.result) continue
      apiFetch(`/api/dead-letters/${encodeURIComponent(c.id)}/check`, { method: 'POST', silent: true })
        .then((res) => res.json() as Promise<CheckResult>)
        .catch((): CheckResult => ({ checked: false, reachable: false }))
        .then((result) => {
          if (cancelled) return
          setChecks((prev) => prev.map((p) => (p.id === c.id ? { ...p, result } : p)))
        })
    }
    return () => { cancelled = true }
    // The checks run once, for the sinks known when the dialog opened.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const checking = checks.some((c) => !c.result)
  const down = checks.filter((c) => c.result?.checked && !c.result.reachable)
  const plural = (k: number, word: string) => `${k} ${word}${k === 1 ? '' : 's'}`
  const latest = resent.map((r) => r.failed_at).sort().at(-1)

  return (
    <Modal opened onClose={onClose} size="lg" title={`Replay ${plural(n, 'record')}?`}>
      <Stack gap="md">
        <Text size="sm">
          This sends {plural(n, 'message')}, one per record, and covers {plural(covered, 'failed attempt')}. Nothing is sent twice.
        </Text>
        {checks.length > 0 && (
          <Stack gap={6} aria-live="polite">
            {checks.map((c) => {
              const name = c.result?.sink_name || c.sinkID
              if (!c.result) {
                return <Group key={c.sinkID} gap={6}><Loader size={14} /><Text size="sm">Checking {name}…</Text></Group>
              }
              if (!c.result.checked) {
                return <Text key={c.sinkID} size="sm" c="dimmed">{name} could not be checked.</Text>
              }
              return c.result.reachable ? (
                <Group key={c.sinkID} gap={6}>
                  <IconCheck size={16} color="var(--mantine-color-green-7)" />
                  <Text size="sm">{name} answers now.</Text>
                </Group>
              ) : (
                <Group key={c.sinkID} gap={6} align="flex-start" wrap="nowrap">
                  <IconX size={16} color="var(--mantine-color-red-7)" style={{ marginTop: 3 }} />
                  <Text size="sm" c="red.8">{name} does not answer: {c.result.error || 'the connection test failed'}. A replay would fail the same way.</Text>
                </Group>
              )
            })}
          </Stack>
        )}
        {resent.length > 0 && latest && (
          <Alert color="yellow" variant="light" icon={<IconAlertTriangle size={16} />}>
            The source kept re-sending {resent.length === 1 ? 'this record' : `${resent.length} of these records`}. Check that
            {resent.length === 1 ? ' it has' : ' they have'} not already been delivered since {whenLabel(latest)}.
          </Alert>
        )}
        <Group justify="flex-end" gap="xs">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button onClick={() => onConfirm(ids)} loading={busy} disabled={checking} color={down.length ? 'red' : undefined}>
            Send {plural(n, 'message')}{down.length ? ' anyway' : ''}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
