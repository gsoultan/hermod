import { Alert, Badge, Button, Card, Code, Group, Stack, Text, Textarea, ThemeIcon, rem } from '@mantine/core'
import { useState } from 'react'
import { IconAlertTriangle, IconCheck, IconWand } from '@tabler/icons-react'
import { apiJson } from '@/api'

interface Candidate {
  config: Record<string, any>
  label: string
  confidence: 'certain' | 'likely'
  preview: string
}

interface DetectionResult {
  candidates?: Candidate[]
  reason?: string
}

interface DetectSettingsProps {
  /** The key already typed into the node; detection needs it to try decrypting. */
  encryptionKey: string
  /** Applies a detected configuration to the node. */
  onApply: (config: Record<string, any>) => void
}

/**
 * Works out how a value was encrypted by trying to decrypt it.
 *
 * The decrypt node's settings interact — key format decides the key bytes,
 * encoding decides the payload bytes, nonce length decides where the ciphertext
 * starts, tag placement decides which end the tag is on, AAD decides whether
 * authentication can succeed — so one wrong setting is indistinguishable from
 * all of them wrong. Matching an external system by hand means searching that
 * space with no signal to search by.
 *
 * With the key, an authenticated algorithm turns the search into a decision:
 * a candidate either verifies its tag or it does not. That is why a `certain`
 * result is worth trusting and a `likely` one is not — the unauthenticated modes
 * have nothing to verify against, so a match there is only plausible plaintext.
 */
export function DetectSettings({ encryptionKey, onApply }: DetectSettingsProps) {
  const [sample, setSample] = useState('')
  const [result, setResult] = useState<DetectionResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const detect = async () => {
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await apiJson<DetectionResult>('/api/transformations/detect-decryption', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sample, key: encryptionKey }),
      })
      setResult(res)
    } catch (e: any) {
      setError(e?.message || 'Detection failed')
    } finally {
      setBusy(false)
    }
  }

  const candidates = result?.candidates ?? []

  return (
    <Card withBorder radius="md" p="md">
      <Group gap="xs" mb="sm">
        <ThemeIcon variant="light" color="violet" size="sm">
          <IconWand size={rem(14)} />
        </ThemeIcon>
        <Text fw={500} size="sm">
          Detect settings from a sample
        </Text>
      </Group>

      <Stack gap="sm">
        <Textarea
          label="An encrypted value"
          value={sample}
          onChange={(e) => setSample(e.currentTarget.value)}
          placeholder="Paste one encrypted value from the column"
          autosize
          minRows={2}
          maxRows={4}
          size="sm"
          description="Detection works by trying to decrypt it, so it needs the key above to be filled in too."
        />

        <Group>
          <Button
            onClick={detect}
            loading={busy}
            disabled={!sample.trim() || !encryptionKey}
            leftSection={<IconWand size={rem(16)} />}
            size="sm"
            variant="light"
          >
            Detect settings
          </Button>
          {!encryptionKey && (
            <Text size="xs" c="dimmed">
              Enter the encryption key first.
            </Text>
          )}
        </Group>

        {error && (
          <Alert icon={<IconAlertTriangle size="1rem" />} color="red" variant="light">
            <Text size="sm">{error}</Text>
          </Alert>
        )}

        {result && candidates.length === 0 && (
          <Alert icon={<IconAlertTriangle size="1rem" />} color="orange" variant="light">
            <Text size="sm">{result.reason || 'Nothing matched.'}</Text>
          </Alert>
        )}

        {candidates.map((c, i) => (
          <Card key={i} withBorder radius="sm" p="sm">
            <Group justify="space-between" mb={6} wrap="nowrap">
              <Group gap="xs" wrap="nowrap">
                <Badge
                  size="sm"
                  color={c.confidence === 'certain' ? 'green' : 'yellow'}
                  variant="light"
                  leftSection={c.confidence === 'certain' ? <IconCheck size={rem(12)} /> : undefined}
                >
                  {c.confidence}
                </Badge>
                <Text size="sm">{c.label}</Text>
              </Group>
              <Button size="compact-sm" variant="light" onClick={() => onApply(c.config)}>
                Apply
              </Button>
            </Group>

            <Text size="xs" c="dimmed" mb={4}>
              Decrypts to:
            </Text>
            <Code block style={{ fontSize: rem(11), whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {c.preview}
            </Code>
          </Card>
        ))}

        {candidates.some((c) => c.confidence === 'likely') && (
          <Alert icon={<IconAlertTriangle size="1rem" />} color="yellow" variant="light">
            <Text size="sm">
              A <strong>likely</strong> match came from an unauthenticated algorithm, which has no tag
              to verify against — it decrypted to something that looks like text, which on a short
              value can be coincidence. Check the preview is really your data before applying it.
            </Text>
          </Alert>
        )}
      </Stack>
    </Card>
  )
}
