import type { ReactNode } from 'react'
import {
  Card, Stack, Group, Text, TextInput, Select, Badge, Alert, Button, Divider, Radio, Code,
} from '@mantine/core'
import { IconAlertTriangle, IconPlugConnected, IconCheck, IconX } from '@tabler/icons-react'
import type { Conflict, ConflictDecision, ImportResource } from '@/utils/importBundle'

export interface TestState {
  status: 'ok' | 'error'
  message: string
}

interface ImportResourceCardProps {
  kind: 'source' | 'sink'
  resource: ImportResource
  /** Set when this resource's ID already names something on this instance. */
  conflict?: Conflict
  decision: ConflictDecision
  onDecision: (decision: ConflictDecision) => void
  onChange: (patch: Partial<ImportResource>) => void
  vhostOptions: string[]
  onTest?: () => void
  testing?: boolean
  testState?: TestState | null
  /** Set when this name is already held by a different record on this instance. */
  nameError?: string
  /** The type-specific connection fields, supplied by the wizard. */
  children: ReactNode
}

/**
 * One source or sink from the bundle, ready to be edited before it is written.
 *
 * The card exists because importing used to be a single POST of whatever the
 * file said: a bundle from another instance arrived with that instance's
 * hostnames and credentials, and the first sign of trouble was the workflow
 * failing to start. Everything on this card is a value that is usually wrong
 * on the machine importing it.
 */
export function ImportResourceCard({
  kind,
  resource,
  conflict,
  decision,
  onDecision,
  onChange,
  vhostOptions,
  onTest,
  testing,
  testState,
  nameError,
  children,
}: ImportResourceCardProps) {
  const label = kind === 'source' ? 'Source' : 'Sink'

  return (
    <Card withBorder radius="md" padding="lg">
      <Stack gap="md">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs">
            <Text fw={600}>{resource.name || `Unnamed ${label.toLowerCase()}`}</Text>
            <Badge variant="light" size="sm">{resource.type}</Badge>
          </Group>
          {conflict ? (
            <Badge color="orange" variant="light" leftSection={<IconAlertTriangle size="0.8rem" />}>
              ID already in use
            </Badge>
          ) : (
            <Badge color="green" variant="light">New</Badge>
          )}
        </Group>

        {conflict && (
          <Alert color="orange" variant="light" icon={<IconAlertTriangle size="1rem" />}>
            <Stack gap="xs">
              <Text size="sm">
                This instance already has a {kind} with id <Code>{conflict.id}</Code>, named{' '}
                <strong>{conflict.existingName}</strong>. Decide what happens to it.
              </Text>
              <Radio.Group
                value={decision}
                onChange={(v) => onDecision(v as ConflictDecision)}
                name={`decision-${resource.id}`}
              >
                <Stack gap={6} mt={4}>
                  <Radio
                    value="overwrite"
                    label={`Replace ${conflict.existingName}`}
                    description={`Its configuration is overwritten with what is below. Anything already using this ${kind} starts using these settings.`}
                  />
                  <Radio
                    value="copy"
                    label="Import as a separate copy"
                    description={`A new id is generated and the workflow's references are repointed at it. ${conflict.existingName} is left untouched.`}
                  />
                </Stack>
              </Radio.Group>
            </Stack>
          </Alert>
        )}

        <Group grow align="flex-start">
          <TextInput
            label="Name"
            value={resource.name ?? ''}
            onChange={(e) => onChange({ name: e.currentTarget.value })}
            error={nameError}
            required
          />
          <Select
            label="Virtual host"
            description="Where this lands on this instance."
            data={vhostOptions}
            value={resource.vhost || ''}
            onChange={(v) => onChange({ vhost: v || '' })}
            searchable
          />
        </Group>

        <Divider label="Connection" labelPosition="left" />

        {children}

        {onTest && (
          <Stack gap="xs">
            {testState && (
              <Alert
                color={testState.status === 'ok' ? 'green' : 'red'}
                variant="light"
                icon={testState.status === 'ok' ? <IconCheck size="1rem" /> : <IconX size="1rem" />}
              >
                {testState.message}
              </Alert>
            )}
            <Group justify="flex-end">
              <Button
                variant="light"
                leftSection={<IconPlugConnected size="1rem" />}
                onClick={onTest}
                loading={testing}
              >
                Test connection
              </Button>
            </Group>
          </Stack>
        )}
      </Stack>
    </Card>
  )
}
