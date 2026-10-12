import { useId, useState } from 'react'
import { Badge, Button, Group, List, Stack, Table, Text } from '@mantine/core'
import { IconThumbDown, IconThumbUp } from '@tabler/icons-react'
import { useMutation } from '@tanstack/react-query'
import type { RecommendedItem } from '@/lib/mlModels'
import { postFeedback, type FeedbackOutcome, type RecommendResponse, type ScenarioItem } from '@/lib/mlRecommend'
import { formatMetric } from './metrics'

/** What a recommended thing is called: its name when the data has one, else its id. */
const itemName = (it: ScenarioItem) => (typeof it.fields?.name === 'string' && it.fields.name ? it.fields.name : it.id)

/** A day of a plan, short: "Tue 2026-10-13". */
function dayLabel(date: string): string {
  const d = new Date(`${date}T00:00:00`)
  return Number.isNaN(d.getTime()) ? date : `${d.toLocaleDateString(undefined, { weekday: 'short' })} ${date}`
}

/** Where people say whether a recommendation was useful; absent where there is nothing to report to. */
export interface FeedbackTarget {
  vhost: string
  model: string
  recommendationId: string
}

/** "This was useful / not useful" for one item, posted as accepted or dismissed. */
function Feedback({ target, id }: { target: FeedbackTarget; id: string }) {
  const [said, setSaid] = useState<FeedbackOutcome | null>(null)
  const send = useMutation({
    mutationFn: (outcome: FeedbackOutcome) => postFeedback(target.vhost, target.model, target.recommendationId, [{ id, outcome }]),
    onSuccess: (_r, outcome) => setSaid(outcome),
  })
  if (said) return <Text size="xs" c="dimmed">{said === 'accepted' ? 'Marked useful' : 'Marked not useful'}</Text>
  return (
    <Group gap={4} wrap="nowrap">
      <Button size="compact-xs" variant="subtle" color="green" leftSection={<IconThumbUp size="0.9rem" />} aria-label={`${id} was useful`}
        loading={send.isPending && send.variables === 'accepted'} onClick={() => send.mutate('accepted')}>
        Useful
      </Button>
      <Button size="compact-xs" variant="subtle" color="gray" leftSection={<IconThumbDown size="0.9rem" />} aria-label={`${id} was not useful`}
        loading={send.isPending && send.variables === 'dismissed'} onClick={() => send.mutate('dismissed')}>
        Not useful
      </Button>
      {send.error && <Text size="xs" c="red">Not sent: {(send.error as Error).message}</Text>}
    </Group>
  )
}

/** Ranked items with their reasons, a reorder's due date, and optionally feedback buttons. */
export function ScenarioItemList({ items, feedback, label }: { items: ScenarioItem[]; feedback?: FeedbackTarget; label?: string }) {
  if (!items.length) return <Text size="sm" c="dimmed">Nothing to recommend.</Text>
  return (
    <List spacing="xs" listStyleType="none" aria-label={label}>
      {items.map((it) => (
        <List.Item key={it.id}>
          <Group gap="xs" wrap="wrap" align="baseline">
            <Text size="sm" c="dimmed" w={24}>{it.rank}.</Text>
            <Text size="sm" fw={500}>{itemName(it)}</Text>
            {itemName(it) !== it.id && <Text size="xs" c="dimmed" ff="var(--mantine-font-family-monospace)">{it.id}</Text>}
            {it.due_date && <Badge size="xs" variant="light" tt="none">Due {it.due_date}</Badge>}
            {feedback && <Feedback target={feedback} id={it.id} />}
          </Group>
          {!!it.reasons?.length && (
            <Text size="xs" c="dimmed" pl={32}>{it.reasons.map((r) => r.text).join(' · ')}</Text>
          )}
        </List.Item>
      ))}
    </List>
  )
}

/** One answer of the recommend route: a plan by day for visits, else a ranked list. */
export function RecommendAnswer({ answer, feedback }: { answer: RecommendResponse; feedback?: Omit<FeedbackTarget, 'recommendationId'> }) {
  const result = answer.results?.[0]
  const target = feedback ? { ...feedback, recommendationId: answer.recommendation_id } : undefined
  if (!result) return <Text size="sm" c="dimmed">Nothing to recommend.</Text>
  if (result.plan?.length) return <PlanDays plan={result.plan} feedback={target} />
  return <ScenarioItemList items={result.items} feedback={target} label="Recommendations" />
}

/** A visit plan as a calendar list: each day, its customers and why. */
function PlanDays({ plan, feedback }: { plan: NonNullable<RecommendResponse['results'][number]['plan']>; feedback?: FeedbackTarget }) {
  return (
    <Stack gap="sm">
      {plan.map((day) => <PlanDay key={day.date} date={day.date} items={day.items} feedback={feedback} />)}
    </Stack>
  )
}

function PlanDay({ date, items, feedback }: { date: string; items: ScenarioItem[]; feedback?: FeedbackTarget }) {
  const id = useId()
  return (
    <Stack gap={4}>
      <Text size="sm" fw={600} id={id}>{dayLabel(date)}</Text>
      {items.length ? (
        <List spacing="xs" listStyleType="none" aria-labelledby={id}>
          {items.map((it) => (
            <List.Item key={it.id}>
              <Group gap="xs" wrap="wrap" align="baseline">
                <Text size="sm" fw={500}>{itemName(it)}</Text>
                {itemName(it) !== it.id && <Text size="xs" c="dimmed" ff="var(--mantine-font-family-monospace)">{it.id}</Text>}
                {feedback && <Feedback target={feedback} id={it.id} />}
              </Group>
              {!!it.reasons?.length && <Text size="xs" c="dimmed">{it.reasons.map((r) => r.text).join(' · ')}</Text>}
            </List.Item>
          ))}
        </List>
      ) : (
        <Text size="xs" c="dimmed">No visits.</Text>
      )}
    </Stack>
  )
}

/** A one-table recommender's answer (the predict route): items and scores, the popular fallback marked. */
export function RecommendedList({ items }: { items: RecommendedItem[] }) {
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
