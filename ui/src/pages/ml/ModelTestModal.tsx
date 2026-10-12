import { useState } from 'react'
import { Alert, Badge, Button, Checkbox, Code, Group, Modal, Radio, Stack, Table, Text, TextInput, Textarea } from '@mantine/core'
import { IconAlertCircle, IconPlayerPlay } from '@tabler/icons-react'
import { useMutation } from '@tanstack/react-query'
import { isRecommender, predict, recommend, type MLModel, type RecommendCall, type RecommendedItem } from '@/lib/mlModels'
import { formatMetric } from './metrics'

/** A starting row: the model's declared features, each set to 0. */
function sampleRow(model: MLModel): string {
  const row = Object.fromEntries((model.features ?? []).map((f) => [f, 0]))
  return JSON.stringify(row, null, 2)
}

/** How many items the test asks a recommender for. */
const TEST_K = 10

/**
 * Sends one row to the model, or asks a recommender, and shows what it
 * answered. `initialRow`, such as a row of the dataset it was trained on,
 * replaces the row of zeros to start from; a recommender ignores it.
 */
export function ModelTestModal({
  vhost, model, initialRow, onClose,
}: {
  vhost: string
  model: MLModel
  initialRow?: Record<string, unknown>
  onClose: () => void
}) {
  return (
    <Modal opened onClose={onClose} title={`Test ${model.name}`} centered size="lg">
      {isRecommender(model)
        ? <RecommenderTest vhost={vhost} model={model} />
        : <RowTest vhost={vhost} model={model} initialRow={initialRow} />}
    </Modal>
  )
}

function RowTest({ vhost, model, initialRow }: { vhost: string; model: MLModel; initialRow?: Record<string, unknown> }) {
  const [row, setRow] = useState(() => (initialRow ? JSON.stringify(initialRow, null, 2) : sampleRow(model)))
  const [parseError, setParseError] = useState<string | null>(null)
  const run = useMutation({ mutationFn: (instance: Record<string, unknown>) => predict(vhost, model.name, [instance]) })

  const submit = () => {
    let parsed: unknown
    try {
      parsed = JSON.parse(row)
    } catch {
      setParseError('The row must be a JSON object.')
      return
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setParseError('The row must be a JSON object.')
      return
    }
    setParseError(null)
    run.mutate(parsed as Record<string, unknown>)
  }

  return (
    <Stack gap="md">
      <Textarea label="Row" description="One record as JSON, with the fields the model takes." autosize minRows={4} maxRows={14}
        value={row} onChange={(e) => setRow(e.currentTarget.value)} error={parseError}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
      <Group justify="flex-end">
        <Button leftSection={<IconPlayerPlay size="1rem" />} onClick={submit} loading={run.isPending}>Run</Button>
      </Group>
      {run.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
          {(run.error as Error).message}
        </Alert>
      )}
      {run.data && (
        <Stack gap={4}>
          <Text size="sm" fw={500}>Prediction</Text>
          <Code block>{JSON.stringify(run.data[0], null, 2)}</Code>
        </Stack>
      )}
    </Stack>
  )
}

/** A person's top 10, or the 10 items most like one item. */
function RecommenderTest({ vhost, model }: { vhost: string; model: MLModel }) {
  const [mode, setMode] = useState<'user' | 'item'>('user')
  const [id, setId] = useState('')
  const [excludeSeen, setExcludeSeen] = useState(true)
  const run = useMutation({ mutationFn: (call: RecommendCall) => recommend(vhost, model.name, call) })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!id.trim()) return
    run.mutate(mode === 'user'
      ? { user: id.trim(), k: TEST_K, exclude_seen: excludeSeen }
      : { item: id.trim(), k: TEST_K })
  }

  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        <Radio.Group value={mode} onChange={(v) => { setMode(v as typeof mode); setId(''); run.reset() }}>
          <Group gap="lg">
            <Radio value="user" label="Top 10 for a person" />
            <Radio value="item" label="Similar items" />
          </Group>
        </Radio.Group>
        {mode === 'user' ? (
          <>
            <TextInput label="Person" description="Their id, as in the training data." placeholder="customer id" required
              value={id} onChange={(e) => setId(e.currentTarget.value)} />
            <Checkbox label="Leave out what they already had" checked={excludeSeen}
              onChange={(e) => setExcludeSeen(e.currentTarget.checked)} />
          </>
        ) : (
          <TextInput label="Item" description="Its id, as in the training data." placeholder="product id" required
            value={id} onChange={(e) => setId(e.currentTarget.value)} />
        )}
        <Group justify="flex-end">
          <Button type="submit" leftSection={<IconPlayerPlay size="1rem" />} loading={run.isPending} disabled={!id.trim()}>
            {mode === 'user' ? 'Recommend' : 'Find similar'}
          </Button>
        </Group>
        {run.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
            {(run.error as Error).message}
          </Alert>
        )}
        {run.data && <RecommendedList items={run.data} />}
      </Stack>
    </form>
  )
}

function RecommendedList({ items }: { items: RecommendedItem[] }) {
  if (!items.length) return <Text size="sm" c="dimmed">No items.</Text>
  const fallback = items.every((i) => i.reason === 'popular')
  return (
    <Stack gap={4}>
      {fallback && (
        <Text size="xs" c="dimmed">The model does not know this id, so these are the most popular items.</Text>
      )}
      <Table withRowBorders={false} verticalSpacing={2} fz="sm">
        <Table.Tbody>
          {items.map((it, n) => (
            <Table.Tr key={it.item}>
              <Table.Td c="dimmed" w={32}>{n + 1}</Table.Td>
              <Table.Td>{it.item}</Table.Td>
              <Table.Td ff="var(--mantine-font-family-monospace)" c="dimmed">{formatMetric(it.score)}</Table.Td>
              <Table.Td>{it.reason && <Badge size="xs" variant="light" color="gray" tt="none">{it.reason}</Badge>}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}
