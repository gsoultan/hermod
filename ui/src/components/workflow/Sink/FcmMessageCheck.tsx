import { useState } from 'react';
import { Alert, Button, Code, Group, Paper, Progress, Stack, Text, Textarea } from '@mantine/core';
import { IconAlertTriangle, IconCircleCheck, IconPlayerPlay } from '@tabler/icons-react';
import { apiFetch } from '@/api';

/** What POST /api/sinks/fcm/preview answers with. */
interface FcmPreview {
  /** The FCM message as it goes on the wire. Absent when the sink refuses the row. */
  message?: {
    token?: string;
    topic?: string;
    condition?: string;
    notification?: { title?: string; body?: string; image?: string };
    data?: Record<string, string>;
    [key: string]: unknown;
  };
  recipients: number;
  /** The data map's size before the oversize choice acted on it, and after. */
  data_bytes: number;
  sent_data_bytes: number;
  limit: number;
  largest?: Array<{ key: string; bytes: number }>;
  /** The failure a run would report for this row. */
  refused?: string;
  sample: Record<string, unknown>;
}

const bytes = (n: number) => n.toLocaleString('en-US');

/** A row the check can be run against: an object, not a list or a scalar. */
function asSampleRow(value: unknown): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  return JSON.stringify(value, null, 2);
}

function destination(preview: FcmPreview): string {
  const message = preview.message;
  if (!message) return '';
  if (message.topic) return `topic ${message.topic}`;
  if (message.condition) return `devices matching ${message.condition}`;
  return preview.recipients === 1 ? '1 device' : `${bytes(preview.recipients)} devices`;
}

/**
 * Builds the message a sample row would become and shows it, with its data
 * weighed against FCM's 4096-byte limit.
 *
 * The limit is why this is here. The form's default sends the whole row, FCM
 * refuses a data map over the limit, and the first thing that used to say a row
 * was too wide was the run that failed on it. The server builds the message
 * with the sink's own code, so the size shown is the size a run sends — there
 * is no second implementation of it here to drift.
 */
export function FcmMessageCheck({
  config,
  incomingPayload,
}: {
  config: Record<string, any>;
  /** The row the editor says reaches this sink. */
  incomingPayload?: unknown;
}) {
  const [busy, setBusy] = useState(false);
  const [asked, setAsked] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<FcmPreview | null>(null);
  // Empty means "whatever the server uses as an example"; its answer fills this
  // in so it can be edited into the operator's own row.
  const [sampleText, setSampleText] = useState('');

  const run = async (sample: string) => {
    let row: unknown;
    if (sample.trim()) {
      try {
        row = JSON.parse(sample);
      } catch {
        setPreview(null);
        setError('The sample row is not valid JSON.');
        return;
      }
    }

    // Rendering a message authenticates to nothing, so the service account
    // stays in the form rather than travelling for a question about a title.
    const { credentials_json: _withheld, ...rest } = config;

    setBusy(true);
    setError(null);
    try {
      // silent: the answer belongs here, beside the fields that caused it, not
      // in a toast that outlives the question.
      const res = await apiFetch('/api/sinks/fcm/preview', {
        method: 'POST',
        silent: true,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'fcm', config: rest, sample: row }),
      });
      const data: FcmPreview = await res.json();
      setPreview(data);
      if (!sample.trim() && data.sample) {
        setSampleText(JSON.stringify(data.sample, null, 2));
      }
    } catch (err: any) {
      setPreview(null);
      setError(err?.data?.error || err?.message || 'The check failed.');
    } finally {
      setBusy(false);
    }
  };

  // Seeded on the first press rather than in useState: the editor's sample
  // often arrives after this form has mounted.
  const start = () => {
    const row = sampleText.trim() ? sampleText : asSampleRow(incomingPayload);
    if (row !== sampleText) setSampleText(row);
    setAsked(true);
    void run(row);
  };

  if (!asked) {
    return (
      <Group justify="space-between" align="center">
        <Text size="xs" c="dimmed" style={{ flex: '1 1 14rem' }}>
          See the message one row becomes, and whether its data fits, before a workflow runs.
        </Text>
        <Button variant="light" size="xs" leftSection={<IconPlayerPlay size="0.9rem" />} onClick={start}>
          Check with a sample row
        </Button>
      </Group>
    );
  }

  const over = preview ? preview.data_bytes > preview.limit : false;
  const used = preview ? Math.min(100, (preview.data_bytes / preview.limit) * 100) : 0;
  const shortened = preview ? !preview.refused && preview.sent_data_bytes < preview.data_bytes : false;

  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm" aria-live="polite">
        {error && (
          <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="The message could not be built">
            {error}
          </Alert>
        )}

        {preview && (
          <>
            <Stack gap={4}>
              <Group justify="space-between">
                <Text size="sm" fw={500}>
                  Data: {bytes(preview.data_bytes)} of {bytes(preview.limit)} bytes
                </Text>
                {over && (
                  <Text size="xs" c="red">
                    {bytes(preview.data_bytes - preview.limit)} over
                  </Text>
                )}
              </Group>
              <Progress
                value={used}
                color={over ? 'red' : used >= 80 ? 'yellow' : 'teal'}
                aria-label="Share of FCM's data limit this row uses"
              />
              {preview.largest && preview.largest.length > 0 && (
                <Text size="xs" c="dimmed">
                  Largest: {preview.largest.map((entry) => `${entry.key} (${bytes(entry.bytes)} bytes)`).join(', ')}
                </Text>
              )}
            </Stack>

            {preview.refused ? (
              <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="This row would fail">
                {preview.refused}
              </Alert>
            ) : (
              <Alert color="teal" icon={<IconCircleCheck size="1rem" />} title="This row would be sent">
                <Stack gap={4}>
                  <Text size="sm">
                    <Text span fw={600}>To: </Text>
                    {destination(preview)}
                  </Text>
                  {preview.message?.notification ? (
                    <>
                      {preview.message.notification.title && (
                        <Text size="sm">
                          <Text span fw={600}>Title: </Text>
                          {preview.message.notification.title}
                        </Text>
                      )}
                      {preview.message.notification.body && (
                        <Text size="sm">
                          <Text span fw={600}>Body: </Text>
                          {preview.message.notification.body}
                        </Text>
                      )}
                    </>
                  ) : (
                    <Text size="sm">No notification: the app receives the data and shows nothing itself.</Text>
                  )}
                  {shortened && (
                    <Text size="sm">
                      {preview.sent_data_bytes === 0
                        ? `The data was dropped: it was ${bytes(preview.data_bytes)} bytes.`
                        : `The data was shortened from ${bytes(preview.data_bytes)} bytes to ${bytes(preview.sent_data_bytes)} to fit.`}
                    </Text>
                  )}
                </Stack>
              </Alert>
            )}

            {preview.message && (
              <Code block aria-label="The message as FCM receives it" style={{ maxHeight: 220, overflow: 'auto' }}>
                {JSON.stringify(preview.message, null, 2)}
              </Code>
            )}
          </>
        )}

        <Textarea
          label="Sample row"
          description="The row the message is built from. Edit it to try your own."
          value={sampleText}
          onChange={(e) => setSampleText(e.currentTarget.value)}
          autosize
          minRows={3}
          maxRows={8}
          spellCheck={false}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-xs)' } }}
        />
        <Group justify="flex-end">
          <Button size="xs" variant="light" loading={busy} onClick={() => void run(sampleText)}>
            Check again
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
