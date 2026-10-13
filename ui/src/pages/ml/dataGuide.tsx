import { Drawer, List, Stack, Table, Text, Title } from '@mantine/core'
import { LIMITS } from '@/lib/mlDataGuide'
import { ROLES, SCENARIOS, scenarioRoles, type RoleKey } from '@/lib/mlRecommend'

const n = (v: number) => v.toLocaleString('en-US')

const ROLE_ORDER: RoleKey[] = ['transactions', 'customers', 'products', 'visits', 'salesmen']

/**
 * "How to prepare your data", for someone who has never trained a model: what
 * each scenario needs, what each table looks like, and the amounts the data
 * health check asks for. It says what docs/ml-recommendations-data-guide.md
 * says, and the thresholds are the health check's (ml-plan section 13.3).
 */
export function DataGuideDrawer({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  return (
    <Drawer opened={opened} onClose={onClose} position="right" size="lg" title="How to prepare your data">
      <Stack gap="lg">
        <Text size="sm">
          Hermod learns from what your customers really did. You give it a few tables, each with a job (a role), and
          tell it which column is which. The tables do not have to come from the same database: Hermod joins them by the
          ids you point at.
        </Text>

        <Stack gap="xs">
          <Title order={4}>What each kind of recommendation needs</Title>
          <Table fz="sm" verticalSpacing={4} aria-label="What each kind of recommendation needs">
            <Table.Thead>
              <Table.Tr><Table.Th>Recommendation</Table.Th><Table.Th>Needs</Table.Th><Table.Th>Helps</Table.Th></Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {SCENARIOS.map((s) => {
                const roles = scenarioRoles(s.value)
                const names = (req: boolean) => roles.filter((r) => r.required === req).map((r) => ROLES[r.role].label).join(', ') || '—'
                return (
                  <Table.Tr key={s.value}>
                    <Table.Td>{s.label}</Table.Td>
                    <Table.Td>{names(true)}</Table.Td>
                    <Table.Td>{names(false)}</Table.Td>
                  </Table.Tr>
                )
              })}
            </Table.Tbody>
          </Table>
        </Stack>

        <Stack gap="xs">
          <Title order={4}>The tables</Title>
          {ROLE_ORDER.map((role) => (
            <Stack key={role} gap={2}>
              <Text size="sm" fw={600}>{ROLES[role].label}</Text>
              <Text size="sm" c="dimmed">{ROLES[role].what}</Text>
              <Text size="xs">Columns: {ROLES[role].columns.map((c) => c.label.toLowerCase()).join(', ')}.</Text>
            </Stack>
          ))}
        </Stack>

        <Stack gap="xs">
          <Title order={4}>How much data is enough</Title>
          <List size="sm" spacing={4}>
            <List.Item>
              Product recommendations: at least {n(LIMITS.minCustomersProducts)} customers, with about{' '}
              {LIMITS.minOrdersPerCustomerProducts} or more orders each.
            </List.Item>
            <List.Item>
              Visits and reorder timing: at least {n(LIMITS.minCustomersTiming)} customers, with {LIMITS.minOrdersPerCustomer} or more orders each.
            </List.Item>
            <List.Item>
              History at least twice the planning period: to plan the next 3 months, give at least 6 months of orders.
            </List.Item>
            <List.Item>Fewer than {n(LIMITS.maxProducts)} products. With more, group them (by family or size) first.</List.Item>
            <List.Item>Each card's "What data do I need?" has the exact amounts for that question.</List.Item>
          </List>
        </Stack>

        <Stack gap="xs">
          <Title order={4}>What the health check looks for</Title>
          <List size="sm" spacing={4}>
            <List.Item>The same order line twice (duplicates).</List.Item>
            <List.Item>Dates in the future, usually a wrong date format.</List.Item>
            <List.Item>Negative quantities: returns belong in their own table, or should be left out.</List.Item>
            <List.Item>Ids that do not match between tables, such as orders for a customer missing from the customers table.</List.Item>
          </List>
          <Text size="sm" c="dimmed">
            Green is fine, amber is worth fixing, red stops training until it is fixed. Each one says how.
          </Text>
        </Stack>

        <Stack gap="xs">
          <Title order={4}>Tips</Title>
          <List size="sm" spacing={4}>
            <List.Item>One row per product on an order, not one row per order.</List.Item>
            <List.Item>Use the same ids everywhere: the customer id in the orders must be the one in the customers table.</List.Item>
            <List.Item>Write dates as 2026-09-01 (year, month, day).</List.Item>
            <List.Item>
              Fill a dataset from a database query and give it a refresh schedule under Datasets, so every retraining
              uses fresh data.
            </List.Item>
            <List.Item>Each table on the next step has an example and a CSV template to download.</List.Item>
          </List>
        </Stack>
      </Stack>
    </Drawer>
  )
}
