import { useEffect, useRef, useState } from 'react';
import {
  Accordion,
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  Paper,
  Progress,
  Stack,
  Table,
  Text,
  Textarea,
  ThemeIcon,
} from '@mantine/core';
import { IconAlertTriangle, IconBell, IconPhoto, IconSend } from '@tabler/icons-react';
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

/** How long the form must sit still before it is previewed again. */
const SETTLE_MS = 350;

const count = (n: number) => n.toLocaleString('en-US');

function isJsonOrBlank(text: string): boolean {
  if (!text.trim()) return true;
  try {
    JSON.parse(text);
    return true;
  } catch {
    return false;
  }
}

/** A row the preview can be built from: an object, not a list or a scalar. */
function asSampleRow(value: unknown): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  return JSON.stringify(value, null, 2);
}

function destination(preview: FcmPreview): string {
  const message = preview.message;
  if (!message) return '';
  if (message.topic) return `everyone subscribed to topic ${message.topic}`;
  if (message.condition) return `devices matching ${message.condition}`;
  return preview.recipients === 1 ? '1 device' : `${count(preview.recipients)} devices`;
}

/** The notification as a phone would draw it, or a note that it draws nothing. */
function NotificationMock({ notification }: { notification?: { title?: string; body?: string; image?: string } }) {
  if (!notification) {
    return (
      <Paper radius="lg" p="sm" withBorder style={{ borderStyle: 'dashed' }}>
        <Text size="sm" fw={500}>
          Silent push
        </Text>
        <Text size="xs" c="dimmed">
          Nothing appears on screen. The app receives the data in the background and decides what to show.
        </Text>
      </Paper>
    );
  }
  return (
    <Paper radius="lg" p="sm" bg="var(--mantine-color-default-hover)" style={{ maxWidth: 420 }}>
      <Group gap={6} mb={4}>
        <ThemeIcon size={16} radius="sm" color="orange" aria-hidden>
          <IconBell size={11} />
        </ThemeIcon>
        <Text size="xs" c="dimmed">
          Your app · now
        </Text>
      </Group>
      <Group wrap="nowrap" align="flex-start" justify="space-between" gap="sm">
        <div style={{ minWidth: 0 }}>
          {notification.title && (
            <Text size="sm" fw={600} lineClamp={1}>
              {notification.title}
            </Text>
          )}
          {notification.body && (
            <Text size="sm" lineClamp={3}>
              {notification.body}
            </Text>
          )}
        </div>
        {notification.image && (
          // The URL, not the picture: loading an operator's arbitrary URL into
          // the editor is a request to a host nobody chose to contact.
          <ThemeIcon variant="light" size="xl" radius="md" title={notification.image} aria-label="Has an image">
            <IconPhoto size="1.2rem" />
          </ThemeIcon>
        )}
      </Group>
    </Paper>
  );
}

/**
 * The message one row becomes, kept up to date as the form changes.
 *
 * It used to sit behind a "Check with a sample row" button below the fields,
 * and most people never pressed it — so the first sign a data map was too big,
 * or a field name was wrong, was the run that failed on it. The server builds
 * the message with the sink's own code, so what is shown is what a run sends;
 * there is no second implementation here to drift.
 */
export function FcmMessagePreview({
  config,
  incomingPayload,
}: {
  config: Record<string, any>;
  /** The row the editor says reaches this sink. */
  incomingPayload?: unknown;
}) {
  const editorRow = asSampleRow(incomingPayload);
  // The sample is the editor's row until someone edits it; after that it is
  // theirs, and a new row arriving from the editor does not overwrite it.
  const [ownSample, setOwnSample] = useState<string | null>(null);
  const sampleText = ownSample ?? editorRow;

  const [answering, setAnswering] = useState(false);
  const [answerError, setAnswerError] = useState<string | null>(null);
  const [answer, setAnswer] = useState<FcmPreview | null>(null);

  // A sample that is not JSON is said straight away, and asks the server
  // nothing; the last answer is not shown as if it were about this row.
  const sampleInvalid = !isJsonOrBlank(sampleText);
  const preview = sampleInvalid ? null : answer;
  const error = sampleInvalid ? 'The sample row is not valid JSON.' : answerError;
  const busy = !sampleInvalid && answering;

  // Rendering a message authenticates to nothing, so the service account
  // stays in the form rather than travelling for a question about a title.
  const { credentials_json: _withheld, ...rest } = config;
  const configKey = JSON.stringify(rest);

  // Only the newest request may answer: typing quickly sends several, and they
  // need not come back in order.
  const latest = useRef(0);

  useEffect(() => {
    if (sampleInvalid) {
      // Bumped so a request already in flight cannot answer over this.
      latest.current++;
      return;
    }
    const row: unknown = sampleText.trim() ? JSON.parse(sampleText) : undefined;

    const id = ++latest.current;
    const timer = setTimeout(async () => {
      setAnswering(true);
      try {
        // silent: the answer belongs here, beside the fields that caused it,
        // not in a toast that outlives the question.
        const res = await apiFetch('/api/sinks/fcm/preview', {
          method: 'POST',
          silent: true,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ type: 'fcm', config: JSON.parse(configKey), sample: row }),
        });
        const data: FcmPreview = await res.json();
        if (id !== latest.current) return;
        setAnswer(data);
        setAnswerError(null);
      } catch (err: any) {
        if (id !== latest.current) return;
        setAnswer(null);
        setAnswerError(err?.data?.error || err?.message || 'The preview failed.');
      } finally {
        if (id === latest.current) setAnswering(false);
      }
    }, SETTLE_MS);
    return () => clearTimeout(timer);
  }, [configKey, sampleText, sampleInvalid]);

  const over = preview ? preview.data_bytes > preview.limit : false;
  const used = preview ? Math.min(100, (preview.data_bytes / preview.limit) * 100) : 0;
  const shortened = preview ? !preview.refused && preview.sent_data_bytes < preview.data_bytes : false;
  const data = preview?.message?.data ?? {};
  const dataKeys = Object.keys(data).sort();

  // With no row of its own, the server says which example it used.
  const shownSample = sampleText || (preview?.sample ? JSON.stringify(preview.sample, null, 2) : '');
  const sampleSource =
    ownSample !== null
      ? 'Built from your sample row.'
      : editorRow
        ? 'Built from the row the previous step sends here.'
        : 'Built from an example row. Test the workflow to preview with your own data.';

  // The package prefix is for logs; the reader already knows which sink this is.
  const refusal = preview?.refused?.replace(/^fcm sink: /, '');
  // With no recipient entered the refusal is true but not news: it is a form
  // still being filled in, and calling it a failure in red alarms for nothing.
  const noRecipient = !config.device_token && !config.topic && !config.condition;
  const awaitingRecipient = Boolean(refusal) && noRecipient;

  let status: { label: string; color: string } | null = null;
  if (error) status = { label: 'Cannot build yet', color: 'gray' };
  else if (awaitingRecipient) status = { label: 'Needs a recipient', color: 'gray' };
  else if (refusal) status = { label: 'Would fail', color: 'red' };
  else if (preview) status = { label: 'Ready to send', color: 'teal' };

  return (
    <Paper withBorder radius="md" p="md" component="section" aria-label="Message preview">
      <Stack gap="sm" aria-live="polite">
        <Group justify="space-between" gap="xs">
          <Group gap="xs">
            <Text fw={600} size="sm">
              Preview
            </Text>
            {busy && <Loader size={14} aria-label="Updating the preview" />}
          </Group>
          {status && (
            <Badge color={status.color} variant="light">
              {status.label}
            </Badge>
          )}
        </Group>
        <Text size="xs" c="dimmed">
          {sampleSource}
        </Text>

        {error && (
          <Alert color="gray" icon={<IconAlertTriangle size="1rem" />} title="The message cannot be built">
            {error}
          </Alert>
        )}

        {refusal &&
          (awaitingRecipient ? (
            <Alert color="gray" icon={<IconSend size="1rem" />} title="Who should receive it?">
              <Text size="sm">Choose devices, a topic or a combination of topics under Recipients.</Text>
              <Text size="xs" c="dimmed" mt={4}>
                {refusal}
              </Text>
            </Alert>
          ) : (
            <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="This row would fail">
              {refusal}
            </Alert>
          ))}

        {preview?.message && (
          <>
            <Group gap={6} wrap="nowrap">
              <IconSend size="0.9rem" aria-hidden />
              <Text size="sm">
                <Text span fw={600}>
                  To:{' '}
                </Text>
                {destination(preview)}
              </Text>
            </Group>
            <NotificationMock notification={preview.message.notification} />
          </>
        )}

        {preview && (
          <Stack gap={6}>
            <Group justify="space-between" gap="xs">
              <Text size="sm" fw={500}>
                App data
              </Text>
              <Text size="xs" c={over ? 'red' : 'dimmed'}>
                {count(preview.data_bytes)} of {count(preview.limit)} bytes
                {over && ` — ${count(preview.data_bytes - preview.limit)} over`}
              </Text>
            </Group>
            <Progress
              size="sm"
              value={used}
              color={over ? 'red' : used >= 80 ? 'yellow' : 'teal'}
              aria-label="Share of FCM's data limit this row uses"
            />
            {preview.refused && preview.largest && preview.largest.length > 0 && (
              <Text size="xs" c="dimmed">
                Largest: {preview.largest.map((entry) => `${entry.key} (${count(entry.bytes)} bytes)`).join(', ')}
              </Text>
            )}
            {shortened && (
              <Text size="xs">
                {preview.sent_data_bytes === 0
                  ? `The data is dropped: it was ${count(preview.data_bytes)} bytes.`
                  : `The data is shortened from ${count(preview.data_bytes)} bytes to ${count(preview.sent_data_bytes)} to fit.`}
              </Text>
            )}
            {preview.message &&
              (dataKeys.length > 0 ? (
                <Table.ScrollContainer minWidth={260} maxHeight={240}>
                  <Table striped withTableBorder verticalSpacing={4} fz="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>Key</Table.Th>
                        <Table.Th>Value</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {dataKeys.map((key) => (
                        <Table.Tr key={key}>
                          <Table.Td ff="monospace" style={{ whiteSpace: 'nowrap', verticalAlign: 'top' }}>
                            {key}
                          </Table.Td>
                          <Table.Td ff="monospace" style={{ wordBreak: 'break-all' }}>
                            <Text span inherit lineClamp={2} title={data[key]}>
                              {data[key]}
                            </Text>
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              ) : (
                <Text size="xs" c="dimmed">
                  No data: the app receives the notification alone.
                </Text>
              ))}
          </Stack>
        )}

        <Accordion variant="contained" radius="md" chevronPosition="left" multiple>
          <Accordion.Item value="sample">
            <Accordion.Control>
              <Text size="sm">Sample row</Text>
            </Accordion.Control>
            <Accordion.Panel>
              <Stack gap="xs">
                <Textarea
                  aria-label="Sample row"
                  description="The row the preview is built from. Edit it to try another."
                  value={shownSample}
                  onChange={(e) => setOwnSample(e.currentTarget.value)}
                  autosize
                  minRows={3}
                  maxRows={10}
                  spellCheck={false}
                  styles={{
                    input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-xs)' },
                  }}
                />
                {ownSample !== null && (
                  <Group justify="flex-end">
                    <Button size="compact-xs" variant="default" onClick={() => setOwnSample(null)}>
                      {editorRow ? "Use the previous step's row" : 'Use the example row'}
                    </Button>
                  </Group>
                )}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
          {preview?.message && (
            <Accordion.Item value="wire">
              <Accordion.Control>
                <Text size="sm">Message as FCM receives it</Text>
              </Accordion.Control>
              <Accordion.Panel>
                <Code block aria-label="The message as FCM receives it" style={{ maxHeight: 260, overflow: 'auto' }}>
                  {JSON.stringify(preview.message, null, 2)}
                </Code>
              </Accordion.Panel>
            </Accordion.Item>
          )}
        </Accordion>
      </Stack>
    </Paper>
  );
}
