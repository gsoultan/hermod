import { IconClipboard, IconEye, IconEyeOff, IconKey, IconReload } from '@tabler/icons-react';
import { useState } from 'react'
import type { FC } from 'react'
import { Button, Group, NumberInput, Select, PasswordInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'import { apiFetch } from '../api'

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
      notifications.show({
        color: 'green',
        title: 'Token generated',
        message: 'A new secure token has been generated.',
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
    <Group align="end" gap="sm" wrap="nowrap">
      <PasswordInput
        label={label}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        visible={revealed}
        onVisibilityChange={setRevealed}
        leftSection={<IconKey size="1rem" />}
        style={{ flex: 1 }}
      />
      <NumberInput
        label="Len"
        min={8}
        max={64}
        step={8}
        value={length}
        onChange={(v: string | number) => setLength(typeof v === 'number' ? v : defaultLength)}
        style={{ width: 90 }}
      />
      <Select
        label="Enc"
        data={[
          { value: 'base64url', label: 'base64url' },
          { value: 'hex', label: 'hex' },
        ]}
        value={encoding}
        onChange={(v: string | null) => setEncoding((v as any) || 'base64url')}
        style={{ width: 120 }}
      />
      <Tooltip label="Generate">
        <Button onClick={handleGenerate} loading={loading} variant="light" aria-label="Generate token">
          <IconReload size="1rem" />
        </Button>
      </Tooltip>
      <Tooltip label={revealed ? 'Hide' : 'Reveal'}>
        <Button onClick={() => setRevealed((r) => !r)} variant="light" aria-label="Toggle visibility">
          {revealed ? <IconEyeOff size="1rem" /> : <IconEye size="1rem" />}
        </Button>
      </Tooltip>
      <Tooltip label="Copy">
        <Button onClick={handleCopy} variant="light" aria-label="Copy token">
          <IconClipboard size="1rem" />
        </Button>
      </Tooltip>
    </Group>
  )
}


