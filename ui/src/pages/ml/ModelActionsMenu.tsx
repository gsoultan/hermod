import { useId, type ReactNode } from 'react'
import { ActionIcon, Badge, Menu, Text } from '@mantine/core'
import {
  IconActivityHeartbeat, IconCpu, IconDots, IconHistory, IconKey, IconPencil, IconPlayerPlay, IconRepeat, IconSchool, IconTrash,
} from '@tabler/icons-react'
import { isTrainedModel, type MLModel } from '@/lib/mlModels'

export type ModelAction = 'test' | 'versions' | 'key' | 'monitoring' | 'scoring' | 'train' | 'retrain' | 'edit' | 'delete'

/** One item: its label is its name; when off, the reason is shown under it and read with it. */
function Item({ label, icon, onClick, blocked, color, right }: {
  label: string
  icon: ReactNode
  onClick: () => void
  /** Why it cannot be used now. */
  blocked?: string
  color?: string
  right?: ReactNode
}) {
  const reasonId = useId()
  return (
    <Menu.Item aria-label={label} leftSection={icon} rightSection={right} color={color} onClick={onClick}
      disabled={!!blocked} aria-describedby={blocked ? reasonId : undefined}>
      {label}
      {blocked && <Text size="xs" c="dimmed" id={reasonId}>{blocked}</Text>}
    </Menu.Item>
  )
}

/**
 * Everything one can do with a model, behind one "…" button per row, each
 * named for what it does. A model trained here also has its versions, where it
 * is scored and its retraining; one served elsewhere can be edited. Training
 * again is shown while the worker is missing, off, with the reason.
 */
export function ModelActionsMenu({ model, trainBlocked, onAction }: {
  model: MLModel
  /** Why training cannot start now; undefined when it can. */
  trainBlocked?: string
  onAction: (action: ModelAction) => void
}) {
  const trained = isTrainedModel(model)
  const on = (text: string) => <Badge size="xs" variant="light" tt="none">{text}</Badge>
  return (
    <Menu position="bottom-end" shadow="md" width={280} withinPortal>
      <Menu.Target>
        <ActionIcon variant="subtle" color="gray" radius="md" aria-label={`More actions for ${model.name}`}>
          <IconDots size="1.2rem" />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>
        <Item label="Try it with one row" icon={<IconPlayerPlay size="1rem" />} onClick={() => onAction('test')} />
        {trained && <Item label="Versions and going live" icon={<IconHistory size="1rem" />} onClick={() => onAction('versions')} />}
        <Item label="Serving key for apps" icon={<IconKey size="1rem" />} onClick={() => onAction('key')}
          right={model.serving ? on('On') : undefined} />
        <Item label="Monitoring" icon={<IconActivityHeartbeat size="1rem" />} onClick={() => onAction('monitoring')} />
        {trained && (
          <>
            <Item label="Where it is scored" icon={<IconCpu size="1rem" />} onClick={() => onAction('scoring')}
              right={model.scoring === 'in_process' ? on('In-process') : undefined} />
            <Menu.Divider />
            <Item label="Train again" icon={<IconSchool size="1rem" />} onClick={() => onAction('train')} blocked={trainBlocked} />
            <Item label="Retrain automatically" icon={<IconRepeat size="1rem" />} onClick={() => onAction('retrain')}
              blocked={trainBlocked} right={model.retrain ? on('On') : undefined} />
          </>
        )}
        {!trained && <Item label="Edit connection" icon={<IconPencil size="1rem" />} onClick={() => onAction('edit')} />}
        <Menu.Divider />
        <Item label="Delete model" icon={<IconTrash size="1rem" />} color="red" onClick={() => onAction('delete')} />
      </Menu.Dropdown>
    </Menu>
  )
}
