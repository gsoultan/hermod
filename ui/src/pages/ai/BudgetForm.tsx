import { useState } from 'react'
import {
  ActionIcon, Alert, Button, Group, NumberInput, Paper, Stack, Text, TextInput, Title,
} from '@mantine/core'
import { IconAlertCircle, IconPlus, IconTrash } from '@tabler/icons-react'
import { ANY_MODEL, type AIBudget, type AIModelPrice, type AIWorkflowCap } from '@/lib/aiBudget'

// An empty NumberInput reports ''; a budget holds no limit as 0 or absent.
const num = (v: string | number) => (typeof v === 'number' && v > 0 ? v : undefined)

/**
 * The vhost's budget: monthly limits for the whole vhost, the prices costs
 * are worked out from, and caps for single workflows. Read-only for a Viewer.
 */
export function BudgetForm({ budget, readOnly, saving, error, onSave }: {
  budget: AIBudget
  readOnly: boolean
  saving: boolean
  error?: Error | null
  onSave: (b: AIBudget) => void
}) {
  const [tokens, setTokens] = useState<number | undefined>(budget.monthly_tokens)
  const [cost, setCost] = useState<number | undefined>(budget.monthly_cost)
  const [currency, setCurrency] = useState(budget.currency ?? '')
  const [prices, setPrices] = useState<AIModelPrice[]>(budget.prices ?? [])
  const [caps, setCaps] = useState<AIWorkflowCap[]>(budget.workflows ?? [])

  const setPrice = (i: number, p: Partial<AIModelPrice>) => setPrices((ps) => ps.map((x, j) => (j === i ? { ...x, ...p } : x)))
  const setCap = (i: number, c: Partial<AIWorkflowCap>) => setCaps((cs) => cs.map((x, j) => (j === i ? { ...x, ...c } : x)))

  const save = () => onSave({
    disabled: budget.disabled,
    monthly_tokens: tokens,
    monthly_cost: cost,
    currency: currency.trim() || undefined,
    prices: prices.filter((p) => p.model.trim()).map((p) => ({ ...p, model: p.model.trim() })),
    workflows: caps.filter((c) => c.workflow_id.trim()).map((c) => ({ ...c, workflow_id: c.workflow_id.trim() })),
  })

  return (
    <Paper p="md" withBorder radius="md">
      <Stack gap="md">
        <Title order={4}>Budget</Title>
        <Text size="sm" c="dimmed">
          Empty is no limit. A call is checked before it is made and counted after it answers, so the call that
          crosses a limit is let through and only the next one is refused: a month can overshoot by one call.
        </Text>
        <Group grow align="flex-start">
          <NumberInput label="Monthly token budget" description="Input and output tokens of the whole vhost"
            min={0} allowDecimal={false} thousandSeparator="," value={tokens ?? ''} onChange={(v) => setTokens(num(v))} disabled={readOnly} />
          <NumberInput label="Monthly cost budget" description="Worked out from the prices below"
            min={0} decimalScale={2} value={cost ?? ''} onChange={(v) => setCost(num(v))} disabled={readOnly} />
          <TextInput label="Currency" description="A label only; nothing is converted" placeholder="USD" maxLength={8}
            value={currency} onChange={(e) => setCurrency(e.currentTarget.value)} disabled={readOnly} />
        </Group>

        <Stack gap="xs">
          <Text fw={500} size="sm">Prices per million tokens</Text>
          <Text size="xs" c="dimmed">
            Model {ANY_MODEL} prices every model the list does not name; a cost limit needs it.
          </Text>
          {prices.map((p, i) => (
            <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
              <TextInput aria-label={`Model ${i + 1}`} placeholder={`model id or ${ANY_MODEL}`} value={p.model} style={{ flex: 2 }}
                onChange={(e) => setPrice(i, { model: e.currentTarget.value })} disabled={readOnly} />
              <NumberInput aria-label={`Input price ${i + 1}`} placeholder="input" min={0} decimalScale={6} style={{ flex: 1 }}
                value={p.input_per_million} onChange={(v) => setPrice(i, { input_per_million: num(v) ?? 0 })} disabled={readOnly} />
              <NumberInput aria-label={`Output price ${i + 1}`} placeholder="output" min={0} decimalScale={6} style={{ flex: 1 }}
                value={p.output_per_million} onChange={(v) => setPrice(i, { output_per_million: num(v) ?? 0 })} disabled={readOnly} />
              {!readOnly && (
                <ActionIcon variant="light" color="red" aria-label={`Remove price ${i + 1}`} onClick={() => setPrices((ps) => ps.filter((_, j) => j !== i))}>
                  <IconTrash size="1rem" />
                </ActionIcon>
              )}
            </Group>
          ))}
          {!readOnly && (
            <Button variant="subtle" size="xs" w="fit-content" leftSection={<IconPlus size="0.9rem" />}
              onClick={() => setPrices((ps) => [...ps, { model: ps.length === 0 ? ANY_MODEL : '', input_per_million: 0, output_per_million: 0 }])}>
              Add price
            </Button>
          )}
        </Stack>

        <Stack gap="xs">
          <Text fw={500} size="sm">Workflow caps</Text>
          <Text size="xs" c="dimmed">A workflow's own monthly limit, inside the vhost's.</Text>
          {caps.map((c, i) => (
            <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
              <TextInput aria-label={`Workflow ID ${i + 1}`} placeholder="workflow id" value={c.workflow_id} style={{ flex: 2 }}
                onChange={(e) => setCap(i, { workflow_id: e.currentTarget.value })} disabled={readOnly} />
              <NumberInput aria-label={`Token cap ${i + 1}`} placeholder="tokens" min={0} allowDecimal={false} style={{ flex: 1 }}
                value={c.monthly_tokens ?? ''} onChange={(v) => setCap(i, { monthly_tokens: num(v) })} disabled={readOnly} />
              <NumberInput aria-label={`Cost cap ${i + 1}`} placeholder="cost" min={0} decimalScale={2} style={{ flex: 1 }}
                value={c.monthly_cost ?? ''} onChange={(v) => setCap(i, { monthly_cost: num(v) })} disabled={readOnly} />
              {!readOnly && (
                <ActionIcon variant="light" color="red" aria-label={`Remove workflow cap ${i + 1}`} onClick={() => setCaps((cs) => cs.filter((_, j) => j !== i))}>
                  <IconTrash size="1rem" />
                </ActionIcon>
              )}
            </Group>
          ))}
          {!readOnly && (
            <Button variant="subtle" size="xs" w="fit-content" leftSection={<IconPlus size="0.9rem" />}
              onClick={() => setCaps((cs) => [...cs, { workflow_id: '' }])}>
              Add workflow cap
            </Button>
          )}
        </Stack>

        {error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The budget was not saved">{error.message}</Alert>
        )}
        {!readOnly && (
          <Group justify="flex-end">
            <Button onClick={save} loading={saving}>Save budget</Button>
          </Group>
        )}
      </Stack>
    </Paper>
  )
}
