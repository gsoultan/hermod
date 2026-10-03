import { useState, type ReactNode } from 'react';
import {
  Accordion,
  Alert,
  Badge,
  Button,
  Code,
  FileButton,
  Fieldset,
  Group,
  NumberInput,
  Radio,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { IconAlertTriangle, IconBrandFirebase, IconCircleCheck, IconUpload } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';
import { InsertFieldButton } from '../Transformation/configs/enrichment/apiLookup/InsertFieldButton';
import { fieldToken } from '../Transformation/configs/enrichment/apiLookup/jsonText';
import { FcmAndroidOptions, FcmApnsOptions, FcmWebpushOptions } from './FcmPlatformOptions';
import { FcmDataSection } from './FcmDataSection';
import { FcmMessagePreview } from './FcmMessagePreview';

interface FcmSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
  /** The fields the editor says reach this sink; offered as `{{ }}` tokens. */
  availableFields?: Array<{ path: string; type?: string }>;
  /** The row the editor says reaches this sink; what the preview is built from. */
  incomingPayload?: unknown;
}

type Target = 'token' | 'topic' | 'condition';

const TARGET_KEY: Record<Target, string> = { token: 'device_token', topic: 'topic', condition: 'condition' };

/** A value the sink would act on: present, and not a switch that is off. */
const isSet = (value: unknown) => typeof value === 'string' && value.trim() !== '' && value !== 'false';

const countSet = (config: Record<string, unknown>, belongs: (key: string) => boolean) =>
  Object.keys(config).filter((key) => belongs(key) && isSet(config[key])).length;

/**
 * What a pasted service account says: the project it sends to, or what is
 * wrong with it. Said beside the field, because the backend's version of the
 * same news arrives as a failed save or a failed first message.
 */
function checkCredentials(credentials: string): { project: string; problem: string } {
  if (!credentials.trim()) return { project: '', problem: '' };
  let parsed: any;
  try {
    parsed = JSON.parse(credentials);
  } catch {
    return { project: '', problem: 'This is not valid JSON. Paste the whole key file, from { to }, or upload it.' };
  }
  if (!parsed || typeof parsed !== 'object' || typeof parsed.private_key !== 'string') {
    return {
      project: '',
      problem:
        'This JSON has no private_key, so it is not a service account key. Use Project settings → Service accounts → Generate new private key.',
    };
  }
  if (typeof parsed.project_id !== 'string' || !parsed.project_id) {
    return { project: '', problem: 'This key names no project_id. Paste the whole file as Firebase downloaded it.' };
  }
  return { project: parsed.project_id, problem: '' };
}

/** A text field that is a template, with the upstream fields one click away. */
function TemplateInput({
  label,
  value,
  onChange,
  fieldPaths,
  ...rest
}: {
  label: string;
  value: string;
  onChange: (next: string) => void;
  fieldPaths: string[];
  placeholder?: string;
  description?: ReactNode;
  required?: boolean;
}) {
  return (
    <TextInput
      label={label}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
      rightSection={
        fieldPaths.length > 0 ? (
          <InsertFieldButton
            compact
            label={`Insert field into ${label}`}
            fieldPaths={fieldPaths}
            onPick={(path) => onChange(value + fieldToken(path))}
          />
        ) : undefined
      }
      {...rest}
    />
  );
}

/** One foldable group of optional settings, saying how many of them are set. */
function OptionsSection({
  value,
  title,
  count,
  children,
}: {
  value: string;
  title: string;
  count: number;
  children: ReactNode;
}) {
  return (
    <Accordion.Item value={value}>
      <Accordion.Control>
        <Group gap="xs">
          <Text size="sm" fw={500}>
            {title}
          </Text>
          {count > 0 && (
            <Badge size="sm" variant="light">
              {count} set
            </Badge>
          )}
        </Group>
      </Accordion.Control>
      <Accordion.Panel>{children}</Accordion.Panel>
    </Accordion.Item>
  );
}

/**
 * Firebase Cloud Messaging.
 *
 * Keys match `FromMap` in `pkg/comm/sink/fcm/config.go`; the factory hands the
 * whole config map to it, so a field named here is a field the sink reads.
 * TestUIFormMatchesConfigKeys holds the two together and reads every
 * `Fcm*.tsx` beside this file.
 *
 * The form is in the order the decisions are made — which project, who
 * receives it, what they see, what the app is handed — and the per-platform
 * options, which most sinks never touch, are folded away under it.
 */
export function FcmSinkConfig({ config, updateConfig, availableFields = [], incomingPayload }: FcmSinkConfigProps) {
  const action = config.action || 'send';
  const sending = action === 'send';
  const usingADC = config.use_default_credentials === 'true';
  const credentials = checkCredentials(config.credentials_json || '');
  const fieldPaths = availableFields.map((field) => field.path);

  // FCM accepts exactly one destination per message, so the form offers one
  // choice rather than three fields the backend would then have to refuse.
  //
  // The choice is state, seeded from the config, and not derived from it.
  // Derived, it was "whichever key holds a value": choosing Topic cleared the
  // other two, found all three empty, and fell back to Device token — the
  // picker snapped back and a topic could never be entered.
  const [target, setTargetState] = useState<Target>(() =>
    config.condition ? 'condition' : config.topic ? 'topic' : 'token',
  );
  const keepOnly = (kept: Target) => {
    for (const key of Object.values(TARGET_KEY)) {
      if (key !== TARGET_KEY[kept] && config[key]) updateConfig(key, '');
    }
  };
  const setTarget = (next: Target) => {
    setTargetState(next);
    keepOnly(next);
  };
  // The subscribe actions take a device list and a topic together. Back on
  // "send" that is two destinations, one of them in a field no longer shown.
  const setAction = (next: string) => {
    updateConfig('action', next);
    if (next === 'send') keepOnly(target);
  };

  const androidCount = countSet(config, (key) => key.startsWith('android_'));
  const apnsCount = countSet(config, (key) => key.startsWith('apns_'));
  const webpushCount = countSet(config, (key) => key.startsWith('webpush_'));
  const deliveryKeys = sending ? ['analytics_label', 'timeout', 'max_data_bytes', 'batch'] : ['timeout', 'batch'];
  const deliveryCount = countSet(config, (key) => deliveryKeys.includes(key));

  // A section that already holds settings opens with the form, so folding the
  // options away never hides what a saved sink does. Read once: a section must
  // not spring open because a field in another one was typed into.
  const [openSections] = useState(() =>
    [
      androidCount > 0 && 'android',
      apnsCount > 0 && 'apns',
      webpushCount > 0 && 'webpush',
      deliveryCount > 0 && 'delivery',
    ].filter((section): section is string => Boolean(section)),
  );

  return (
    <Stack gap="md">
      <Fieldset legend="Firebase project" radius="md">
        <Stack gap="md">
          <Textarea
            label="Service account key"
            placeholder='{"type":"service_account","project_id":"…","private_key":"…"}'
            minRows={4}
            autosize
            maxRows={8}
            value={config.credentials_json || ''}
            onChange={(e) => updateConfig('credentials_json', e.currentTarget.value)}
            description="Firebase console → Project settings → Service accounts → Generate new private key. Paste the file's contents or upload it."
            error={usingADC ? undefined : credentials.problem || undefined}
            leftSection={<IconBrandFirebase size="1rem" />}
            styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-xs)' } }}
            required={!usingADC}
            disabled={usingADC}
          />
          {!usingADC && (
            <Group justify="space-between" gap="xs">
              {credentials.project ? (
                <Group gap={6}>
                  <IconCircleCheck size="1rem" color="var(--mantine-color-teal-6)" aria-hidden />
                  <Text size="sm">
                    Messages go to the project <Code>{credentials.project}</Code>
                  </Text>
                </Group>
              ) : (
                <span />
              )}
              <FileButton
                accept="application/json,.json"
                onChange={(file) => {
                  void file?.text().then((text) => updateConfig('credentials_json', text.trim()));
                }}
              >
                {(props) => (
                  <Button {...props} size="xs" variant="light" leftSection={<IconUpload size="0.9rem" />}>
                    Upload key file
                  </Button>
                )}
              </FileButton>
            </Group>
          )}
          <Switch
            label="Use the machine's ambient Google credentials instead"
            checked={usingADC}
            onChange={(e) => updateConfig('use_default_credentials', e.currentTarget.checked ? 'true' : 'false')}
            description="Application Default Credentials. Only for a host with a workload identity; it needs the project named explicitly below."
          />
          {usingADC && (
            <TextInput
              label="Project ID"
              placeholder="my-firebase-project"
              value={config.project_id || ''}
              onChange={(e) => updateConfig('project_id', e.currentTarget.value)}
              description="Ambient credentials do not say which Firebase project to push to, so this is required."
              required
            />
          )}
        </Stack>
      </Fieldset>

      <Select
        label="What each message does"
        value={action}
        onChange={(v) => setAction(v || 'send')}
        data={[
          { value: 'send', label: 'Send a message' },
          { value: 'subscribe', label: 'Subscribe the devices to a topic' },
          { value: 'unsubscribe', label: 'Unsubscribe the devices from a topic' },
        ]}
        allowDeselect={false}
        description="The subscribe actions turn a device-registration table into an FCM audience: an insert subscribes, a delete unsubscribes."
      />

      {sending ? (
        <>
          <Fieldset legend="Recipients" radius="md">
            <Stack gap="md">
              {/* Radios in a wrapping row, not a segmented control: in a narrow
                  drawer a segmented control cannot wrap and clipped its last
                  option off the edge. */}
              <Radio.Group aria-label="Send to" value={target} onChange={(v) => setTarget(v as Target)}>
                <Group gap="lg">
                  <Radio value="token" label="Specific devices" />
                  <Radio value="topic" label="A topic" />
                  <Radio value="condition" label="A combination of topics" />
                </Group>
              </Radio.Group>
              {target === 'token' && (
                <TemplateInput
                  label="Device token"
                  placeholder="{{.fcm_token}}"
                  value={config.device_token || ''}
                  onChange={(v) => updateConfig('device_token', v)}
                  fieldPaths={fieldPaths}
                  description="The field holding each device's FCM registration token — the one your app gets from Firebase and stores. A field with several, separated by commas, reaches up to 500 devices."
                />
              )}
              {target === 'topic' && (
                <TemplateInput
                  label="Topic name"
                  placeholder="orders-{{.region}}"
                  value={config.topic || ''}
                  onChange={(v) => updateConfig('topic', v)}
                  fieldPaths={fieldPaths}
                  description="Every device subscribed to it receives the message. Letters, digits and - _ . ~ %."
                />
              )}
              {target === 'condition' && (
                <TextInput
                  label="Condition"
                  placeholder="'orders' in topics && !('muted' in topics)"
                  value={config.condition || ''}
                  onChange={(e) => updateConfig('condition', e.currentTarget.value)}
                  description="A true-or-false expression over topic names, up to five topics."
                />
              )}
              <Text size="xs" c="dimmed">
                Advanced: an earlier step that sets <Code>fcm_token</Code>, <Code>fcm_topic</Code> or{' '}
                <Code>fcm_condition</Code> metadata overrides this for that message.
              </Text>
            </Stack>
          </Fieldset>

          <Fieldset legend="Notification" radius="md">
            <Stack gap="md">
              <Text size="sm" c="dimmed">
                What people see on their lock screen.
              </Text>
              <FormRow cols={2}>
                <TemplateInput
                  label="Title"
                  placeholder="Order {{.id}} shipped"
                  value={config.title || ''}
                  onChange={(v) => updateConfig('title', v)}
                  fieldPaths={fieldPaths}
                />
                <TemplateInput
                  label="Body"
                  placeholder="{{.customer}} — {{.total}}"
                  value={config.body || ''}
                  onChange={(v) => updateConfig('body', v)}
                  fieldPaths={fieldPaths}
                />
              </FormRow>
              <TemplateInput
                label="Image URL"
                placeholder="https://cdn.example.com/{{.sku}}.png"
                value={config.image_url || ''}
                onChange={(v) => updateConfig('image_url', v)}
                fieldPaths={fieldPaths}
              />
              <Text size="xs" c="dimmed">
                Leave the title and body empty for a silent push: nothing is shown, and the app gets the data below.
                Use the <Code>{'{ }'}</Code> button to insert a field; <Code>{'{{.column}}'}</Code> is replaced by that
                column of the row. A column that may be missing is written <Code>{'{{index . "name"}}'}</Code>.
              </Text>
            </Stack>
          </Fieldset>

          <FcmDataSection config={config} updateConfig={updateConfig} availableFields={availableFields} />

          <FcmMessagePreview config={config} incomingPayload={incomingPayload} />

          <Switch
            label="Dry run"
            checked={config.dry_run === 'true'}
            onChange={(e) => updateConfig('dry_run', e.currentTarget.checked ? 'true' : 'false')}
            description="Every message is validated by FCM and delivered to nobody. Use it to prove a workflow addresses the right devices before it wakes them."
          />
        </>
      ) : (
        <Fieldset legend="Devices and topic" radius="md">
          <FormRow cols={2}>
            <TemplateInput
              label="Device tokens"
              placeholder="{{.fcm_token}}"
              value={config.device_token || ''}
              onChange={(v) => updateConfig('device_token', v)}
              fieldPaths={fieldPaths}
              description="The devices to move. Commas separate several, up to 1000 per message."
              required
            />
            <TemplateInput
              label="Topic"
              placeholder="orders-{{.region}}"
              value={config.topic || ''}
              onChange={(v) => updateConfig('topic', v)}
              fieldPaths={fieldPaths}
              description="The topic to move them to."
              required
            />
          </FormRow>
        </Fieldset>
      )}

      {/* Unmounted when folded: thirty inputs nobody is looking at are thirty
          inputs a screen reader and a test would still walk through. */}
      <Accordion multiple variant="contained" radius="md" keepMounted={false} defaultValue={openSections}>
        {sending && (
          <>
            <OptionsSection value="android" title="Android options" count={androidCount}>
              <FcmAndroidOptions config={config} updateConfig={updateConfig} />
            </OptionsSection>
            <OptionsSection value="apns" title="Apple (APNs) options" count={apnsCount}>
              <FcmApnsOptions config={config} updateConfig={updateConfig} />
            </OptionsSection>
            <OptionsSection value="webpush" title="Web push options" count={webpushCount}>
              <FcmWebpushOptions config={config} updateConfig={updateConfig} />
            </OptionsSection>
          </>
        )}
        <OptionsSection value="delivery" title="Delivery options" count={deliveryCount}>
          <Stack gap="md">
            <FormRow cols={sending ? 3 : 1}>
              <TextInput
                label="Timeout"
                placeholder="30s"
                value={config.timeout || ''}
                onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
                description="Bounds one call. Empty means no sink-imposed deadline."
              />
              {sending && (
                <TextInput
                  label="Analytics label"
                  placeholder="orders_v2"
                  value={config.analytics_label || ''}
                  onChange={(e) => updateConfig('analytics_label', e.currentTarget.value)}
                  description="Tags the send in Firebase analytics."
                />
              )}
              {sending && (
                <NumberInput
                  label="Data size limit (bytes)"
                  placeholder="4096"
                  value={config.max_data_bytes ? Number(config.max_data_bytes) : ''}
                  onChange={(v) => updateConfig('max_data_bytes', v === '' ? '' : String(v))}
                  description="FCM's own limit is 4096. Leave empty for that."
                  min={1}
                  max={4096}
                />
              )}
            </FormRow>
            <Switch
              label="Send in batches"
              checked={config.batch === 'true'}
              onChange={(e) => updateConfig('batch', e.currentTarget.checked ? 'true' : 'false')}
              description="Sends a batch concurrently instead of one message at a time."
            />
            {config.batch === 'true' && (
              <Alert variant="light" color="yellow" icon={<IconAlertTriangle size="1rem" />}>
                <Text size="xs">
                  FCM has no idempotency key. When a batch fails after part of it was delivered, the retry
                  re-delivers to every device that already received it. Without batching the engine retries
                  one message at a time, so only the message that failed is repeated.
                </Text>
              </Alert>
            )}
          </Stack>
        </OptionsSection>
      </Accordion>
    </Stack>
  );
}
