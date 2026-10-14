import { Stack, Switch, Select } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { FormRow } from '@/components/common/FormRow';

interface PlatformOptionsProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
  /** What the preview says is wrong with a setting, to say under it. */
  errorFor?: (key: string) => string | undefined;
}

/**
 * The per-platform halves of an FCM message. Android, APNs and Web Push each
 * mean something different by priority, time to live and grouping, so each has
 * its own block — and each is optional, which is why FcmSinkConfig folds them
 * away rather than laying thirty fields out beside the destination.
 *
 * Keys match `android()`, `apns()` and `webpush()` in
 * `pkg/comm/sink/fcm/config.go`. TestUIFormMatchesConfigKeys reads this file.
 */
export function FcmAndroidOptions({ config, updateConfig, errorFor }: PlatformOptionsProps) {
  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <Select
          label="Priority"
          value={config.android_priority || ''}
          id="fcm-field-android_priority"
          error={errorFor?.('android_priority')}
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
          id="fcm-field-android_ttl"
          error={errorFor?.('android_ttl')}
          onChange={(e) => updateConfig('android_ttl', e.currentTarget.value)}
          description="How long FCM keeps trying. Empty leaves FCM's four weeks."
        />
      </FormRow>
      <FormRow cols={2}>
        <TextInput
          label="Collapse key (template)"
          placeholder="order-{{.id}}"
          value={config.android_collapse_key || ''}
          id="fcm-field-android_collapse_key"
          error={errorFor?.('android_collapse_key')}
          onChange={(e) => updateConfig('android_collapse_key', e.currentTarget.value)}
          description="An undelivered message with the same key is replaced rather than queued."
        />
        <TextInput
          label="Notification channel"
          placeholder="orders"
          value={config.android_channel_id || ''}
          id="fcm-field-android_channel_id"
          error={errorFor?.('android_channel_id')}
          onChange={(e) => updateConfig('android_channel_id', e.currentTarget.value)}
          description="Must already exist in the app, or Android falls back to its default."
        />
      </FormRow>
      <FormRow cols={3}>
        <TextInput
          label="Sound"
          placeholder="default"
          value={config.android_sound || ''}
          id="fcm-field-android_sound"
          error={errorFor?.('android_sound')}
          onChange={(e) => updateConfig('android_sound', e.currentTarget.value)}
        />
        <TextInput
          label="Icon"
          placeholder="ic_notification"
          value={config.android_icon || ''}
          id="fcm-field-android_icon"
          error={errorFor?.('android_icon')}
          onChange={(e) => updateConfig('android_icon', e.currentTarget.value)}
        />
        <TextInput
          label="Colour"
          placeholder="#ff8800"
          value={config.android_color || ''}
          id="fcm-field-android_color"
          error={errorFor?.('android_color')}
          onChange={(e) => updateConfig('android_color', e.currentTarget.value)}
          description="#RRGGBB. FCM refuses any other form."
        />
      </FormRow>
      <FormRow cols={3}>
        <TextInput
          label="Tag (template)"
          placeholder="order-{{.id}}"
          value={config.android_tag || ''}
          id="fcm-field-android_tag"
          error={errorFor?.('android_tag')}
          onChange={(e) => updateConfig('android_tag', e.currentTarget.value)}
          description="A new notification with the same tag replaces the one on screen."
        />
        <TextInput
          label="Click action"
          placeholder="OPEN_ORDER_ACTIVITY"
          value={config.android_click_action || ''}
          id="fcm-field-android_click_action"
          error={errorFor?.('android_click_action')}
          onChange={(e) => updateConfig('android_click_action', e.currentTarget.value)}
        />
        <Select
          label="Notification priority"
          value={config.android_notification_priority || ''}
          id="fcm-field-android_notification_priority"
          error={errorFor?.('android_notification_priority')}
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
        id="fcm-field-android_restricted_package_name"
        error={errorFor?.('android_restricted_package_name')}
        onChange={(e) => updateConfig('android_restricted_package_name', e.currentTarget.value)}
        description="Limits delivery to one app package."
      />
    </Stack>
  );
}

export function FcmApnsOptions({ config, updateConfig, errorFor }: PlatformOptionsProps) {
  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <Select
          label="Priority"
          value={config.apns_priority || ''}
          id="fcm-field-apns_priority"
          error={errorFor?.('apns_priority')}
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
          id="fcm-field-apns_expiration"
          error={errorFor?.('apns_expiration')}
          onChange={(e) => updateConfig('apns_expiration', e.currentTarget.value)}
          description="How long APNs stores an undelivered push. Sent as an absolute time."
        />
      </FormRow>
      <FormRow cols={3}>
        <TextInput
          label="Collapse ID (template)"
          placeholder="order-{{.id}}"
          value={config.apns_collapse_id || ''}
          id="fcm-field-apns_collapse_id"
          error={errorFor?.('apns_collapse_id')}
          onChange={(e) => updateConfig('apns_collapse_id', e.currentTarget.value)}
        />
        <TextInput
          label="Sound"
          placeholder="default"
          value={config.apns_sound || ''}
          id="fcm-field-apns_sound"
          error={errorFor?.('apns_sound')}
          onChange={(e) => updateConfig('apns_sound', e.currentTarget.value)}
        />
        <TextInput
          label="Badge (template)"
          placeholder="{{.unread_count}}"
          value={config.apns_badge || ''}
          id="fcm-field-apns_badge"
          error={errorFor?.('apns_badge')}
          onChange={(e) => updateConfig('apns_badge', e.currentTarget.value)}
          description="Must render to a whole number."
        />
      </FormRow>
      <FormRow cols={2}>
        <TextInput
          label="Category"
          placeholder="ORDER_ACTIONS"
          value={config.apns_category || ''}
          id="fcm-field-apns_category"
          error={errorFor?.('apns_category')}
          onChange={(e) => updateConfig('apns_category', e.currentTarget.value)}
          description="Selects the actions the app registered for this notification."
        />
        <TextInput
          label="Thread ID (template)"
          placeholder="order-{{.id}}"
          value={config.apns_thread_id || ''}
          id="fcm-field-apns_thread_id"
          error={errorFor?.('apns_thread_id')}
          onChange={(e) => updateConfig('apns_thread_id', e.currentTarget.value)}
          description="Groups notifications in Notification Centre."
        />
      </FormRow>
      <Switch
        label="Background push (content-available)"
        checked={config.apns_content_available === 'true'}
        id="fcm-field-apns_content_available"
        error={errorFor?.('apns_content_available')}
        onChange={(e) => updateConfig('apns_content_available', e.currentTarget.checked ? 'true' : 'false')}
        description="Wakes the app to fetch rather than showing an alert. Leave the title and body empty."
      />
      <Switch
        label="Allow the app to rewrite the alert (mutable-content)"
        checked={config.apns_mutable_content === 'true'}
        id="fcm-field-apns_mutable_content"
        error={errorFor?.('apns_mutable_content')}
        onChange={(e) => updateConfig('apns_mutable_content', e.currentTarget.checked ? 'true' : 'false')}
        description="Required for a notification service extension to decrypt or decorate the message."
      />
    </Stack>
  );
}

export function FcmWebpushOptions({ config, updateConfig, errorFor }: PlatformOptionsProps) {
  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <TextInput
          label="Link (template)"
          placeholder="https://app.example.com/orders/{{.id}}"
          value={config.webpush_link || ''}
          id="fcm-field-webpush_link"
          error={errorFor?.('webpush_link')}
          onChange={(e) => updateConfig('webpush_link', e.currentTarget.value)}
          description="Opened when the notification is clicked. Must be https."
        />
        <TextInput
          label="Time to live"
          placeholder="1h"
          value={config.webpush_ttl || ''}
          id="fcm-field-webpush_ttl"
          error={errorFor?.('webpush_ttl')}
          onChange={(e) => updateConfig('webpush_ttl', e.currentTarget.value)}
        />
      </FormRow>
      <FormRow cols={2}>
        <TextInput
          label="Icon URL"
          placeholder="https://cdn.example.com/icon.png"
          value={config.webpush_icon || ''}
          id="fcm-field-webpush_icon"
          error={errorFor?.('webpush_icon')}
          onChange={(e) => updateConfig('webpush_icon', e.currentTarget.value)}
        />
        <TextInput
          label="Badge URL"
          placeholder="https://cdn.example.com/badge.png"
          value={config.webpush_badge || ''}
          id="fcm-field-webpush_badge"
          error={errorFor?.('webpush_badge')}
          onChange={(e) => updateConfig('webpush_badge', e.currentTarget.value)}
        />
      </FormRow>
    </Stack>
  );
}
