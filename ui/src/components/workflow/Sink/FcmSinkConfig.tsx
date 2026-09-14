import {
  TextInput,
  Textarea,
  Stack,
  Divider,
  Switch,
  Select,
  Alert,
  Code,
  Text,
  NumberInput,
} from '@mantine/core';
import { IconBrandFirebase, IconAlertTriangle, IconInfoCircle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface FcmSinkConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * Firebase Cloud Messaging.
 *
 * Keys match `FromMap` in `pkg/comm/sink/fcm/config.go`; the factory hands the
 * whole config map to it, so a field named here is a field the sink reads.
 *
 * Text fields marked as templates are Go templates over the message: the row's
 * own columns plus `id`, `operation`, `table`, `schema` and `metadata`.
 */
export function FcmSinkConfig({ config, updateConfig }: FcmSinkConfigProps) {
  const action = config.action || 'send';
  const sending = action === 'send';
  const usingADC = config.use_default_credentials === 'true';

  // FCM accepts exactly one destination per message, so the form offers one
  // choice rather than three fields the backend would then have to refuse.
  const target = config.condition ? 'condition' : config.topic ? 'topic' : 'token';
  const setTarget = (next: string) => {
    for (const key of ['device_token', 'topic', 'condition']) {
      if (key !== (next === 'token' ? 'device_token' : next)) updateConfig(key, '');
    }
  };

  return (
    <Stack gap="md">
      <Textarea
        label="Service account JSON"
        placeholder='{"type":"service_account","project_id":"…","private_key":"…"}'
        minRows={5}
        autosize
        maxRows={10}
        value={config.credentials_json || ''}
        onChange={(e) => updateConfig('credentials_json', e.currentTarget.value)}
        description="Firebase console → Project settings → Service accounts → Generate new private key. The project it names is the project messages go to."
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

      <Divider my="xs" label="What each message does" labelPosition="left" />

      <Select
        label="Action"
        value={action}
        onChange={(v) => updateConfig('action', v || 'send')}
        data={[
          { value: 'send', label: 'Send a message' },
          { value: 'subscribe', label: 'Subscribe the devices to a topic' },
          { value: 'unsubscribe', label: 'Unsubscribe the devices from a topic' },
        ]}
        description="The subscribe actions turn a device-registration table into an FCM audience: an insert subscribes, a delete unsubscribes."
      />

      {sending ? (
        <>
          <Select
            label="Destination"
            value={target}
            onChange={(v) => setTarget(v || 'token')}
            data={[
              { value: 'token', label: 'Device token' },
              { value: 'topic', label: 'Topic' },
              { value: 'condition', label: 'Condition' },
            ]}
            description="FCM accepts exactly one per message. A message carrying fcm_token, fcm_topic or fcm_condition metadata overrides whatever is set here."
          />
          {target === 'token' && (
            <TextInput
              label="Device token (template)"
              placeholder="{{.fcm_token}}"
              value={config.device_token || ''}
              onChange={(e) => updateConfig('device_token', e.currentTarget.value)}
              description="A column holding the registration token. A value with commas in it fans out as a multicast, up to 500 devices."
            />
          )}
          {target === 'topic' && (
            <TextInput
              label="Topic (template)"
              placeholder="orders-{{.region}}"
              value={config.topic || ''}
              onChange={(e) => updateConfig('topic', e.currentTarget.value)}
              description="Letters, digits and - _ . ~ %. The /topics/ prefix is optional."
            />
          )}
          {target === 'condition' && (
            <TextInput
              label="Condition"
              placeholder="'orders' in topics && !('muted' in topics)"
              value={config.condition || ''}
              onChange={(e) => updateConfig('condition', e.currentTarget.value)}
              description="A boolean expression over topic names, up to five topics."
            />
          )}
        </>
      ) : (
        <FormRow cols={2}>
          <TextInput
            label="Device tokens (template)"
            placeholder="{{.fcm_token}}"
            value={config.device_token || ''}
            onChange={(e) => updateConfig('device_token', e.currentTarget.value)}
            description="The devices to move. Commas separate several, up to 1000 per message."
            required
          />
          <TextInput
            label="Topic (template)"
            placeholder="orders-{{.region}}"
            value={config.topic || ''}
            onChange={(e) => updateConfig('topic', e.currentTarget.value)}
            description="The topic to move them to."
            required
          />
        </FormRow>
      )}

      {sending && (
        <>
          <Divider my="xs" label="Notification" labelPosition="left" />
          <Alert variant="light" color="blue" icon={<IconInfoCircle size="1rem" />}>
            <Text size="xs">
              Leave the title and body empty to send a data-only message, which is what an app that
              draws its own notification wants. Templates see the row's columns plus{' '}
              <Code>id</Code>, <Code>operation</Code>, <Code>table</Code>, <Code>schema</Code> and{' '}
              <Code>metadata</Code>. A field that may be absent is reachable with{' '}
              <Code>{'{{index . "name"}}'}</Code>.
            </Text>
          </Alert>
          <FormRow cols={2}>
            <TextInput
              label="Title (template)"
              placeholder="Order {{.id}} shipped"
              value={config.title || ''}
              onChange={(e) => updateConfig('title', e.currentTarget.value)}
            />
            <TextInput
              label="Body (template)"
              placeholder="{{.customer}} — {{.total}}"
              value={config.body || ''}
              onChange={(e) => updateConfig('body', e.currentTarget.value)}
            />
          </FormRow>
          <TextInput
            label="Image URL (template)"
            placeholder="https://cdn.example.com/{{.sku}}.png"
            value={config.image_url || ''}
            onChange={(e) => updateConfig('image_url', e.currentTarget.value)}
          />

          <Divider my="xs" label="Data payload" labelPosition="left" />
          <FormRow cols={2}>
            <Select
              label="Data"
              value={config.data_mode || 'envelope'}
              onChange={(v) => updateConfig('data_mode', v || 'envelope')}
              data={[
                { value: 'envelope', label: 'Envelope — the formatted row under "payload"' },
                { value: 'fields', label: 'Fields — each column as its own key' },
                { value: 'none', label: 'None — notification only' },
              ]}
              description="FCM data values are always strings; numbers and nested objects are rendered."
            />
            <NumberInput
              label="Size limit (bytes)"
              placeholder="4096"
              value={config.max_data_bytes ? Number(config.max_data_bytes) : ''}
              onChange={(v) => updateConfig('max_data_bytes', v === '' ? '' : String(v))}
              description="FCM's own limit is 4096. Leave empty for that."
              min={1}
              max={4096}
            />
          </FormRow>
          <Select
            label="When the data is too big"
            value={config.on_oversize || 'error'}
            onChange={(v) => updateConfig('on_oversize', v || 'error')}
            data={[
              { value: 'error', label: 'Fail the message' },
              { value: 'truncate', label: 'Shorten the largest values' },
              { value: 'drop', label: 'Send the notification without data' },
            ]}
            description="FCM refuses an oversized message, and it will not be smaller on a retry, so failing it is permanent rather than retried."
          />
          <Textarea
            label="Extra data (JSON)"
            placeholder='{"deeplink":"app://orders/{{.id}}"}'
            minRows={2}
            autosize
            value={config.data_json || ''}
            onChange={(e) => updateConfig('data_json', e.currentTarget.value)}
            description="A JSON object of string values, each a template. Merged over whatever the mode above produced."
          />

          <Divider my="xs" label="Android" labelPosition="left" />
          <FormRow cols={2}>
            <Select
              label="Priority"
              value={config.android_priority || ''}
              onChange={(v) => updateConfig('android_priority', v || '')}
              data={[
                { value: '', label: "FCM's default" },
                { value: 'high', label: 'High — wakes a dozing device' },
                { value: 'normal', label: 'Normal' },
              ]}
              clearable={false}
            />
            <TextInput
              label="Time to live"
              placeholder="10m"
              value={config.android_ttl || ''}
              onChange={(e) => updateConfig('android_ttl', e.currentTarget.value)}
              description="How long FCM keeps trying. Empty leaves FCM's four weeks."
            />
          </FormRow>
          <FormRow cols={2}>
            <TextInput
              label="Collapse key (template)"
              placeholder="order-{{.id}}"
              value={config.android_collapse_key || ''}
              onChange={(e) => updateConfig('android_collapse_key', e.currentTarget.value)}
              description="An undelivered message with the same key is replaced rather than queued."
            />
            <TextInput
              label="Notification channel"
              placeholder="orders"
              value={config.android_channel_id || ''}
              onChange={(e) => updateConfig('android_channel_id', e.currentTarget.value)}
              description="Must already exist in the app, or Android falls back to its default."
            />
          </FormRow>
          <FormRow cols={3}>
            <TextInput
              label="Sound"
              placeholder="default"
              value={config.android_sound || ''}
              onChange={(e) => updateConfig('android_sound', e.currentTarget.value)}
            />
            <TextInput
              label="Icon"
              placeholder="ic_notification"
              value={config.android_icon || ''}
              onChange={(e) => updateConfig('android_icon', e.currentTarget.value)}
            />
            <TextInput
              label="Colour"
              placeholder="#ff8800"
              value={config.android_color || ''}
              onChange={(e) => updateConfig('android_color', e.currentTarget.value)}
              description="#RRGGBB. FCM refuses any other form."
            />
          </FormRow>
          <FormRow cols={3}>
            <TextInput
              label="Tag (template)"
              placeholder="order-{{.id}}"
              value={config.android_tag || ''}
              onChange={(e) => updateConfig('android_tag', e.currentTarget.value)}
              description="A new notification with the same tag replaces the one on screen."
            />
            <TextInput
              label="Click action"
              placeholder="OPEN_ORDER_ACTIVITY"
              value={config.android_click_action || ''}
              onChange={(e) => updateConfig('android_click_action', e.currentTarget.value)}
            />
            <Select
              label="Notification priority"
              value={config.android_notification_priority || ''}
              onChange={(v) => updateConfig('android_notification_priority', v || '')}
              data={[
                { value: '', label: "App's default" },
                { value: 'min', label: 'Min' },
                { value: 'low', label: 'Low' },
                { value: 'default', label: 'Default' },
                { value: 'high', label: 'High' },
                { value: 'max', label: 'Max' },
              ]}
            />
          </FormRow>
          <TextInput
            label="Restricted package name"
            placeholder="com.example.app"
            value={config.android_restricted_package_name || ''}
            onChange={(e) => updateConfig('android_restricted_package_name', e.currentTarget.value)}
            description="Limits delivery to one app package."
          />

          <Divider my="xs" label="Apple (APNs)" labelPosition="left" />
          <FormRow cols={2}>
            <Select
              label="Priority"
              value={config.apns_priority || ''}
              onChange={(v) => updateConfig('apns_priority', v || '')}
              data={[
                { value: '', label: "APNs' default" },
                { value: '10', label: '10 — deliver immediately' },
                { value: '5', label: '5 — power-considerate' },
              ]}
              description="APNs refuses 10 for a background push; pair content-available with 5."
            />
            <TextInput
              label="Expiration"
              placeholder="5m"
              value={config.apns_expiration || ''}
              onChange={(e) => updateConfig('apns_expiration', e.currentTarget.value)}
              description="How long APNs stores an undelivered push. Sent as an absolute time."
            />
          </FormRow>
          <FormRow cols={3}>
            <TextInput
              label="Collapse ID (template)"
              placeholder="order-{{.id}}"
              value={config.apns_collapse_id || ''}
              onChange={(e) => updateConfig('apns_collapse_id', e.currentTarget.value)}
            />
            <TextInput
              label="Sound"
              placeholder="default"
              value={config.apns_sound || ''}
              onChange={(e) => updateConfig('apns_sound', e.currentTarget.value)}
            />
            <TextInput
              label="Badge (template)"
              placeholder="{{.unread_count}}"
              value={config.apns_badge || ''}
              onChange={(e) => updateConfig('apns_badge', e.currentTarget.value)}
              description="Must render to a whole number."
            />
          </FormRow>
          <FormRow cols={2}>
            <TextInput
              label="Category"
              placeholder="ORDER_ACTIONS"
              value={config.apns_category || ''}
              onChange={(e) => updateConfig('apns_category', e.currentTarget.value)}
              description="Selects the actions the app registered for this notification."
            />
            <TextInput
              label="Thread ID (template)"
              placeholder="order-{{.id}}"
              value={config.apns_thread_id || ''}
              onChange={(e) => updateConfig('apns_thread_id', e.currentTarget.value)}
              description="Groups notifications in Notification Centre."
            />
          </FormRow>
          <Switch
            label="Background push (content-available)"
            checked={config.apns_content_available === 'true'}
            onChange={(e) => updateConfig('apns_content_available', e.currentTarget.checked ? 'true' : 'false')}
            description="Wakes the app to fetch rather than showing an alert. Leave the title and body empty."
          />
          <Switch
            label="Allow the app to rewrite the alert (mutable-content)"
            checked={config.apns_mutable_content === 'true'}
            onChange={(e) => updateConfig('apns_mutable_content', e.currentTarget.checked ? 'true' : 'false')}
            description="Required for a notification service extension to decrypt or decorate the message."
          />

          <Divider my="xs" label="Web push" labelPosition="left" />
          <FormRow cols={2}>
            <TextInput
              label="Link (template)"
              placeholder="https://app.example.com/orders/{{.id}}"
              value={config.webpush_link || ''}
              onChange={(e) => updateConfig('webpush_link', e.currentTarget.value)}
              description="Opened when the notification is clicked. Must be https."
            />
            <TextInput
              label="Time to live"
              placeholder="1h"
              value={config.webpush_ttl || ''}
              onChange={(e) => updateConfig('webpush_ttl', e.currentTarget.value)}
            />
          </FormRow>
          <FormRow cols={2}>
            <TextInput
              label="Icon URL"
              placeholder="https://cdn.example.com/icon.png"
              value={config.webpush_icon || ''}
              onChange={(e) => updateConfig('webpush_icon', e.currentTarget.value)}
            />
            <TextInput
              label="Badge URL"
              placeholder="https://cdn.example.com/badge.png"
              value={config.webpush_badge || ''}
              onChange={(e) => updateConfig('webpush_badge', e.currentTarget.value)}
            />
          </FormRow>
        </>
      )}

      <Divider my="xs" label="Delivery" labelPosition="left" />
      <FormRow cols={2}>
        <TextInput
          label="Analytics label"
          placeholder="orders_v2"
          value={config.analytics_label || ''}
          onChange={(e) => updateConfig('analytics_label', e.currentTarget.value)}
          description="Tags the send in Firebase analytics."
        />
        <TextInput
          label="Timeout"
          placeholder="30s"
          value={config.timeout || ''}
          onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
          description="Bounds one call. Empty means no sink-imposed deadline."
        />
      </FormRow>
      <Switch
        label="Dry run"
        checked={config.dry_run === 'true'}
        onChange={(e) => updateConfig('dry_run', e.currentTarget.checked ? 'true' : 'false')}
        description="Every message is validated by FCM and delivered to nobody. Use it to prove a workflow addresses the right devices before it wakes them."
      />
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
  );
}
