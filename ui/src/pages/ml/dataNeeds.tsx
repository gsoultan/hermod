import { useState } from 'react'
import {
  Accordion, Badge, Button, Code, Drawer, Group, List, Select, Stack, Table, Text, ThemeIcon, Title,
} from '@mantine/core'
import { IconAlertTriangle, IconCircleCheck, IconCircleX, IconDownload, IconHelpCircle } from '@tabler/icons-react'
import {
  BIG_TABLE_TIPS, DATABASE_NOTES, GUIDE_KINDS, dataGuide, guideTemplates, guideTitle, type ChecklistItem, type GuideColumn,
  type GuideKind,
} from '@/lib/mlDataGuide'
import type { CheckStatus } from '@/lib/mlRecommend'

const KIND_OPTIONS = GUIDE_KINDS.map((k) => ({ value: k, label: guideTitle(k) }))

/** Saves `csv` as a file named `file`. */
export function saveCsv(file: string, csv: string) {
  try {
    const url = URL.createObjectURL(new Blob([csv + '\n'], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = file
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    // A browser without object URLs has nothing to save to; the example table is still shown.
  }
}

/** "What data do I need?" under a choice card: opens the guide of that kind. */
export function DataNeedsLink({ kind }: { kind: GuideKind }) {
  const [opened, setOpened] = useState(false)
  return (
    <>
      <Button variant="subtle" size="compact-xs" leftSection={<IconHelpCircle size="0.9rem" />} style={{ alignSelf: 'flex-start' }}
        aria-label={`What data do I need for ${guideTitle(kind)}?`} onClick={() => setOpened(true)}>
        What data do I need?
      </Button>
      {opened && <DataNeedsDrawer kind={kind} onClose={() => setOpened(false)} />}
    </>
  )
}

function ColumnsTable({ label, columns, why }: { label: string; columns: GuideColumn[]; why?: boolean }) {
  return (
    <Table.ScrollContainer minWidth={420}>
      <Table fz="sm" verticalSpacing={4} withTableBorder aria-label={label}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Column</Table.Th>
            <Table.Th>{why ? 'Why it helps' : 'What it means'}</Table.Th>
            {!why && <Table.Th>Example</Table.Th>}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {columns.map((c) => (
            <Table.Tr key={c.key}>
              <Table.Td>
                <Text size="sm" fw={500}>{c.name}</Text>
                <Text size="xs" c="dimmed">{c.table}</Text>
              </Table.Td>
              <Table.Td>{why ? c.why : c.means}</Table.Td>
              {!why && <Table.Td><Code>{c.example}</Code></Table.Td>}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  )
}

/**
 * The guide of one kind of model, for someone who has never trained one:
 * the question it answers, the columns it needs and the ones that help, how
 * much data is required and recommended, an example to download, a query
 * for a database and the usual mistakes. Another kind can be picked.
 */
export function DataNeedsDrawer({ kind: initial, onClose }: { kind: GuideKind; onClose: () => void }) {
  const [kind, setKind] = useState<GuideKind>(initial)
  const g = dataGuide(kind)
  return (
    <Drawer opened onClose={onClose} position="right" size="lg" title="What data do I need?">
      <Stack gap="lg">
        <Select label="For" data={KIND_OPTIONS} value={kind} allowDeselect={false} onChange={(v) => v && setKind(v as GuideKind)} />
        <Stack gap={4}>
          <Title order={4}>{g.title}</Title>
          <Text size="sm">{g.question}</Text>
          <Text size="sm" c="dimmed">{g.rows}</Text>
        </Stack>

        <Stack gap="xs">
          <Title order={5}>You need these columns</Title>
          <ColumnsTable label="Required columns" columns={g.required} />
        </Stack>

        {g.optional.length > 0 && (
          <Stack gap="xs">
            <Title order={5}>These make it better</Title>
            <ColumnsTable label="Optional columns" columns={g.optional} why />
          </Stack>
        )}

        <Stack gap="xs">
          <Title order={5}>How much data</Title>
          <Text size="xs" c="dimmed">Required: Hermod will not train with less. Recommended: it trains, but the answers are weaker with less.</Text>
          <Table fz="sm" verticalSpacing={4} withTableBorder aria-label="How much data">
            <Table.Thead><Table.Tr><Table.Th /><Table.Th>Required</Table.Th><Table.Th>Recommended</Table.Th></Table.Tr></Table.Thead>
            <Table.Tbody>
              {g.minimums.map((m) => (
                <Table.Tr key={m.id}>
                  <Table.Td>{m.what}</Table.Td>
                  <Table.Td>{m.required ?? '—'}</Table.Td>
                  <Table.Td>{m.recommended ?? '—'}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Stack>

        {g.terms && (
          <Stack gap="xs">
            <Title order={5}>What the answers mean</Title>
            <List size="sm" spacing={6} aria-label="What the answers mean">
              {g.terms.map((t) => (
                <List.Item key={t.term}><Text span size="sm" fw={600}>{t.term}.</Text> {t.meaning}</List.Item>
              ))}
            </List>
          </Stack>
        )}

        <Stack gap="xs">
          <Title order={5}>Example</Title>
          <Table.ScrollContainer minWidth={420}>
            <Table fz="xs" withTableBorder striped aria-label={`Example: ${g.example.table}`}>
              <Table.Thead><Table.Tr>{g.example.header.map((h) => <Table.Th key={h}>{h}</Table.Th>)}</Table.Tr></Table.Thead>
              <Table.Tbody>
                {g.example.rows.map((row, i) => (
                  <Table.Tr key={i}>{row.map((v, j) => <Table.Td key={j}>{v}</Table.Td>)}</Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
          <Group gap="xs">
            {guideTemplates(kind).map((t) => (
              <Button key={t.file} variant="default" size="xs" leftSection={<IconDownload size="1rem" />} onClick={() => saveCsv(t.file, t.csv)}>
                Download {t.file}
              </Button>
            ))}
          </Group>
        </Stack>

        <Stack gap="xs">
          <Title order={5}>From a database</Title>
          <Code block>{g.query}</Code>
          {g.bigQuery && (
            <>
              <Text size="xs" c="dimmed">For a big table, one row per customer, product and day:</Text>
              <Code block>{g.bigQuery}</Code>
            </>
          )}
        </Stack>

        <Stack gap="xs">
          <Title order={5}>Common mistakes</Title>
          <List size="sm" spacing={4}>
            {g.mistakes.map((m) => <List.Item key={m}>{m}</List.Item>)}
          </List>
        </Stack>
      </Stack>
    </Drawer>
  )
}

const STATUS: Record<CheckStatus, { color: string; label: string; icon: typeof IconCircleCheck }> = {
  green: { color: 'green', label: 'OK', icon: IconCircleCheck },
  amber: { color: 'yellow', label: 'Worth fixing', icon: IconAlertTriangle },
  red: { color: 'red', label: 'Must fix', icon: IconCircleX },
}

/** The checklist of the chosen data against what the model needs: green, amber or red, each with its fix. */
export function DataChecklist({ items, title = 'Your data against what this model needs' }: { items: ChecklistItem[]; title?: string }) {
  return (
    <Stack gap={6}>
      <Text size="sm" fw={500}>{title}</Text>
      <List spacing="xs" listStyleType="none" aria-label={title}>
        {items.map((c) => {
          const s = STATUS[c.status]
          const Icon = s.icon
          return (
            <List.Item key={c.id} data-status={c.status}
              icon={<ThemeIcon color={s.color} variant="light" size="sm" radius="xl"><Icon size="0.9rem" /></ThemeIcon>}>
              <Group gap="xs" wrap="wrap">
                <Text size="sm" fw={500}>{c.label}</Text>
                <Badge size="xs" variant="light" color={s.color} tt="none">{s.label}</Badge>
              </Group>
              {c.detail && <Text size="sm">{c.detail}</Text>}
              {c.fix && <Text size="sm" c="dimmed">{c.fix}</Text>}
            </List.Item>
          )
        })}
      </List>
    </Stack>
  )
}

/** "What will you train with it?": the kind a dataset is for, to tailor the help. */
function KindSelect({ value, onChange }: { value: GuideKind; onChange: (k: GuideKind) => void }) {
  return (
    <Select label="What will you train with it?" description="Only to show which columns it should have." data={KIND_OPTIONS}
      value={value} allowDeselect={false} onChange={(v) => v && onChange(v as GuideKind)} />
  )
}

const columnList = (cols: GuideColumn[]) => cols.filter((c) => c.header).map((c) => c.header).join(', ')

/** What a file should hold for a kind of model, with its CSV template. */
export function FileHelp() {
  const [kind, setKind] = useState<GuideKind>('classification')
  const g = dataGuide(kind)
  const optional = columnList(g.optional)
  return (
    <Stack gap="xs">
      <KindSelect value={kind} onChange={setKind} />
      <Text size="sm">
        {g.rows} It needs the columns {columnList(g.required)}{optional ? `, and if you have them ${optional}` : ''}.
      </Text>
      <Group gap="xs">
        {guideTemplates(kind).map((t) => (
          <Button key={t.file} variant="default" size="xs" leftSection={<IconDownload size="1rem" />} onClick={() => saveCsv(t.file, t.csv)}>
            Download {t.file}
          </Button>
        ))}
      </Group>
    </Stack>
  )
}

/**
 * What a query should return for a kind of model, an example to start from,
 * and what to know about limits and big tables.
 */
export function QueryHelp({ onUseQuery }: { onUseQuery: (query: string) => void }) {
  const [kind, setKind] = useState<GuideKind>('classification')
  const g = dataGuide(kind)
  const optional = columnList(g.optional)
  return (
    <Stack gap="xs">
      <KindSelect value={kind} onChange={setKind} />
      <Text size="sm">
        {g.rows} The query should return {columnList(g.required)}{optional ? `, and if you have them ${optional}` : ''}.
      </Text>
      <Code block aria-label="Example query">{g.query}</Code>
      <Group gap="xs">
        <Button variant="light" size="xs" onClick={() => onUseQuery(g.query)}>Use this example</Button>
        {g.bigQuery && (
          <Button variant="subtle" size="xs" onClick={() => onUseQuery(g.bigQuery as string)}>Use the big-table version</Button>
        )}
      </Group>
      <Accordion variant="contained">
        <Accordion.Item value="limits">
          <Accordion.Control>Limits, and tips for big tables</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="xs">
              <List size="sm" spacing={4}>{DATABASE_NOTES.map((n) => <List.Item key={n}>{n}</List.Item>)}</List>
              <Text size="sm" fw={500}>Big tables</Text>
              <List size="sm" spacing={4}>{BIG_TABLE_TIPS.map((n) => <List.Item key={n}>{n}</List.Item>)}</List>
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}
