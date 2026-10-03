import { useState, type ReactNode } from 'react';
import {
  Accordion,
  Alert,
  Badge,
  Code,
  Fieldset,
  Group,
  Input,
  NumberInput,
  Radio,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core';
import { IconAlertTriangle, IconBrandFirebase } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';
import { InsertFieldButton } from '../Transformation/configs/enrichment/apiLookup/InsertFieldButton';
import { KeyValueEditor } from '../Transformation/configs/enrichment/apiLookup/KeyValueEditor';
import { fieldToken } from '../Transformation/configs/enrichment/apiLookup/jsonText';
import { FcmAndroidOptions, FcmApnsOptions, FcmWebpushOptions } from './FcmPlatformOptions';
import { FcmMessageCheck } from './FcmMessageCheck';

interface FcmSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
  /** The fields the editor says reach this sink; offered as `{{ }}` tokens. */
  availableFields?: Array<{ path: string }>;
  /** The row the editor says reaches this sink; what the check is run against. */
  incomingPayload?: unknown;
}

type Target = 'token' | 'topic' | 'condition';

const TARGET_KEY: Record<Target, string> = { token: 'device_token', topic: 'topic', condition: 'condition' };

/** A value the sink would act on: present, and not a switch that is off. */
const isSet = (value: unknown) => typeof value === 'string' && value.trim() !== '' && value !== 'false';

const countSet = (config: Record<string, unknown>, belongs: (key: string) => boolean) =>
  Object.keys(config).filter((key) => belongs(key) && isSet(config[key])).length;

/** The project a pasted service account names, or '' while it is not JSON yet. */
function projectOf(credentials: string): string {
  try {
    const parsed = JSON.parse(credentials);
    return typeof parsed?.project_id === 'string' ? parsed.project_id : '';
  } catch {
    return '';
  }
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
  const project = projectOf(config.credentials_json || '');
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
            label="Service account JSON"
            placeholder='{"type":"service_account","project_id":"…","private_key":"…"}'
            minRows={4}
            autosize
            maxRows={8}
            value={config.credentials_json || ''}
            onChange={(e) => updateConfig('credentials_json', e.currentTarget.value)}
            description={
              project && !usingADC ? (
                <>
                  Messages go to the project <Code>{project}</Code>.
                </>
              ) : (
                'Firebase console → Project settings → Service accounts → Generate new private key. The project it names is the project messages go to.'
              )
            }
            leftSection={<IconBrandFirebase size="1rem" />}
            required={!usingADC}
            disabled={usingADC}
          />
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
          <Fieldset legend="Who receives it" radius="md">
            <Stack gap="md">
              {/* Radios in a wrapping row, not a segmented control: in a narrow
                  drawer a segmented control cannot wrap and clipped its last
                  option off the edge. */}
              <Radio.Group aria-label="Send to" value={target} onChange={(v) => setTarget(v as Target)}>
                <Group gap="lg">
                  <Radio value="token" label="Devices" />
                  <Radio value="topic" label="A topic" />
                  <Radio value="condition" label="A condition" />
                </Group>
              </Radio.Group>
              {target === 'token' && (
                <TemplateInput
                  label="Device token"
                  placeholder="{{.fcm_token}}"
                  value={config.device_token || ''}
                  onChange={(v) => updateConfig('device_token', v)}
                  fieldPaths={fieldPaths}
                  description="The column holding the device's registration token. Several tokens separated by commas reach up to 500 devices."
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
                Leave this empty only when an earlier step sets <Code>fcm_token</Code>, <Code>fcm_topic</Code> or{' '}
                <Code>fcm_condition</Code> metadata on each message. Metadata always wins over what is set here.
              </Text>
            </Stack>
          </Fieldset>

          <Fieldset legend="What the person sees" radius="md">
            <Stack gap="md">
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
                Leave the title and body empty to send silently: the app receives the data and draws its own
                notification, or none. A <Code>{'{{.column}}'}</Code> is replaced with that column of the row;{' '}
                <Code>id</Code>, <Code>operation</Code>, <Code>table</Code>, <Code>schema</Code> and{' '}
                <Code>metadata</Code> are there too. A column that may be missing is written{' '}
                <Code>{'{{index . "name"}}'}</Code>.
              </Text>
            </Stack>
          </Fieldset>

          <Fieldset legend="Data sent to the app" radius="md">
            <Stack gap="md">
              <Radio.Group
                label="What to send"
                description="FCM accepts at most 4,096 bytes of data, names included, and every value is sent as text."
                value={config.data_mode || 'envelope'}
                onChange={(v) => updateConfig('data_mode', v)}
              >
                <Stack gap="xs" mt="xs">
                  <Radio
                    value="none"
                    label="Only the values listed below"
                    description="Nothing from the row unless you add it. With no values listed, the message is the notification alone."
                  />
                  <Radio
                    value="fields"
                    label="Every column of the row"
                    description="Each column is its own value. The column the message is addressed by is left out."
                  />
                  <Radio
                    value="envelope"
                    label="The whole row as one JSON text"
                    description="Under the name payload, beside id, operation, table and schema. A wide row will not fit."
                  />
                </Stack>
              </Radio.Group>

              <Input.Wrapper
                label="Values to add"
                description="Sent on top of the choice above. A value is text, a {{ }} field, or both."
              >
                <KeyValueEditor
                  value={config.data_json || ''}
                  onChange={(next) => updateConfig('data_json', next)}
                  noun="Value"
                  jsonLabel="Values as JSON"
                  jsonPlaceholder='{"deeplink":"app://orders/{{.id}}"}'
                  emptyHint="No values added. A deep link is the usual one: name it deeplink and give it app://orders/{{.id}}."
                  fieldPaths={fieldPaths}
                />
              </Input.Wrapper>

              <Select
                label="If the data does not fit"
                value={config.on_oversize || 'error'}
                onChange={(v) => updateConfig('on_oversize', v || 'error')}
                data={[
                  { value: 'error', label: 'Fail the message' },
                  { value: 'truncate', label: 'Shorten the largest values until it fits' },
                  { value: 'drop', label: 'Send the notification without the data' },
                ]}
                allowDeselect={false}
                description="A row over the limit is the same size on every attempt, so a failed message stays failed. Shortening keeps id, operation, table and schema whole."
              />
            </Stack>
          </Fieldset>

          <FcmMessageCheck config={config} incomingPayload={incomingPayload} />

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
