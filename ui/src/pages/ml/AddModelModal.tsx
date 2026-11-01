import { useId, type ReactNode } from 'react'
import { Button, Group, Modal, Paper, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import { IconChevronRight, IconPlugConnected, IconSchool } from '@tabler/icons-react'

/** One way to add a model: a large button with what it is and when to pick it. */
function Choice({ icon, title, children, disabled, reason, onClick }: {
  icon: ReactNode
  title: string
  children: ReactNode
  disabled?: boolean
  /** Why it is off, shown under it and read with it. */
  reason?: ReactNode
  onClick: () => void
}) {
  const titleId = useId()
  const textId = useId()
  const reasonId = useId()
  return (
    <Stack gap={4}>
      <UnstyledButton onClick={onClick} disabled={disabled} aria-labelledby={titleId}
        aria-describedby={disabled && reason ? `${textId} ${reasonId}` : textId}
        style={{ borderRadius: 'var(--mantine-radius-md)', opacity: disabled ? 0.6 : 1, cursor: disabled ? 'not-allowed' : 'pointer' }}>
        <Paper withBorder radius="md" p="md">
          <Group wrap="nowrap" align="flex-start" gap="md">
            <ThemeIcon size="xl" radius="md" variant="light" aria-hidden>{icon}</ThemeIcon>
            <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
              <Text fw={600} id={titleId}>{title}</Text>
              <Text size="sm" c="dimmed" id={textId}>{children}</Text>
            </Stack>
            <IconChevronRight size="1.2rem" aria-hidden style={{ alignSelf: 'center' }} />
          </Group>
        </Paper>
      </UnstyledButton>
      {disabled && reason && <Text size="sm" c="dimmed" id={reasonId} component="div">{reason}</Text>}
    </Stack>
  )
}

/**
 * The first step of Add model: train one here, or connect one that already
 * runs somewhere. People could not tell "Add model" from "Train a model"; this
 * asks the question in their words. Training stays offered while the worker
 * is missing, off, with the reason and a way to set it up.
 */
export function AddModelModal({ trainBlocked, onSetup, onTrain, onConnect, onClose }: {
  /** Why training cannot start now; undefined when it can. */
  trainBlocked?: string
  /** Opens the worker setup guide; absent while the worker status is unknown. */
  onSetup?: () => void
  onTrain: () => void
  onConnect: () => void
  onClose: () => void
}) {
  return (
    <Modal opened onClose={onClose} title="Add a model" centered size="lg">
      <Stack gap="md">
        <Text size="sm" c="dimmed">How do you want to add it?</Text>
        <Choice icon={<IconSchool size="1.4rem" />} title="Train a new model from my data" onClick={onTrain}
          disabled={!!trainBlocked}
          reason={trainBlocked && (
            <Group gap="xs">
              <span>{trainBlocked}</span>
              {onSetup && <Button size="compact-sm" variant="subtle" onClick={onSetup}>How to set it up</Button>}
            </Group>
          )}>
          Hermod learns from rows you already have, such as past customers, and predicts the same column for new ones.
          Best when you do not have a model yet.
        </Choice>
        <Choice icon={<IconPlugConnected size="1.4rem" />} title="Connect a model that's already running somewhere" onClick={onConnect}>
          Your team already serves a model on mimir-server, KServe, Triton, MLServer or MLflow. Hermod only needs its address.
        </Choice>
      </Stack>
    </Modal>
  )
}
