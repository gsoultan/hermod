import { IconClipboard, IconKey, IconReload, IconSettings } from '@tabler/icons-react';
import { useState } from 'react'
import type { FC } from 'react'
import { Button, Group, NumberInput, Select, PasswordInput, Tooltip, ActionIcon, Stack, Collapse, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { apiFetch } from '../api'

export interface GenerateTokenProps {
  label?: string
  value: string
  onChange: (val: string) => void
  defaultLength?: number
  defaultEncoding?: 'base64url' | 'hex'
}

export const GenerateToken: FC<GenerateTokenProps> = ({
  label = 'Token',
  value,
  onChange,
  defaultLength = 32,
  defaultEncoding = 'base64url',
}) => {
  const [length, setLength] = useState<number>(defaultLength)
  const [encoding, setEncoding] = useState<'base64url' | 'hex'>(defaultEncoding)
  const [revealed, setRevealed] = useState(false)
  const [loading, setLoading] = useState(false)
  const [showConfig, setShowConfig] = useState(false)

  async function handleGenerate() {
    try {
      setLoading(true)
      const res = await apiFetch('/api/utils/token', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ length, encoding }),
      })
      if (!res.ok) throw new Error('Failed to generate token')
      const json = await res.json()
      onChange(json.token)
      setRevealed(true)
      notifications.show({
        color: 'green',
        title: 'Token generated',
        message: 'A new secure token has been generated and revealed.',
      })
    } catch (e: any) {
      notifications.show({ color: 'red', title: 'Error', message: e?.message || 'Failed to generate token' })
    } finally {
      setLoading(false)
    }
  }

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value || '')
      notifications.show({ color: 'green', title: 'Copied', message: 'Token copied to clipboard' })
    } catch {
      notifications.show({ color: 'red', title: 'Error', message: 'Failed to copy' })
    }
  }

  return (
    <Stack gap="xs">
      <Group align="end" gap="xs" wrap="nowrap">
        <PasswordInput
          label={label}
          value={value}
          onChange={(e) => onChange(e.currentTarget.value)}
          visible={revealed}
          onVisibilityChange={setRevealed}
          leftSection={<IconKey size="1rem" />}
          style={{ flex: 1 }}
          placeholder="Click generate to create a new key"
        />
        <Group gap={5} align="end" wrap="nowrap">
          <Tooltip label="Copy to clipboard">
            <ActionIcon onClick={handleCopy} size="lg" variant="light" color="blue" disabled={!value} aria-label="Copy token">
              <IconClipboard size="1.1rem" />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Generate new key">
            <Button 
              onClick={handleGenerate} 
              loading={loading} 
              variant="filled" 
              leftSection={<IconReload size="1rem" />}
              px="md"
            >
              Generate
            </Button>
          </Tooltip>
          <Tooltip label="Token settings">
            <ActionIcon variant="subtle" color="gray" size="lg" onClick={() => setShowConfig(!showConfig)} aria-label="Toggle token settings">
              <IconSettings size="1.1rem" />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      
      <Collapse in={showConfig}>
        <Group gap="md" p="xs" style={{ border: '1px dashed var(--mantine-color-gray-3)', borderRadius: '4px' }}>
          <NumberInput
            label="Length"
            size="xs"
            min={8}
            max={64}
            step={8}
            value={length}
            onChange={(v) => setLength(typeof v === 'number' ? v : defaultLength)}
            style={{ width: 80 }}
          />
          <Select
            label="Encoding"
            size="xs"
            data={[
              { value: 'base64url', label: 'base64url' },
              { value: 'hex', label: 'hex' },
            ]}
            value={encoding}
            onChange={(v) => setEncoding(v as any || 'base64url')}
            style={{ width: 110 }}
          />
          <Text size="xs" c="dimmed" style={{ flex: 1 }}>
            Configure the length and encoding of the generated secure token.
          </Text>
        </Group>
      </Collapse>
    </Stack>
  )
}


