import {
  TextInput,
  PasswordInput,
  Select,
  NumberInput,
  Stack,
  Divider,
  Alert,
  Text,
} from '@mantine/core';
import { IconSitemap, IconInfoCircle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface MetisSourceConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The metis source polls a BPMN workflow engine for process history: instances,
 * human tasks, or the incidents an operator has to resolve.
 *
 * Keys match `createSourceBase` case "metis" and `pkg/comm/source/metis`.
 */
export function MetisSourceConfig({ config, updateConfig }: MetisSourceConfigProps) {
  const stream = config.stream || 'instances';

  return (
    <Stack gap="md">
      <FormRow cols={2}>
        <TextInput
          label="Engine URL"
          placeholder="https://bpm.example.com"
          value={config.base_url || ''}
          onChange={(e) => updateConfig('base_url', e.currentTarget.value)}
          description="The engine's origin, not a path. Plaintext http is refused unless the host is loopback, because the token travels in a header."
          leftSection={<IconSitemap size="1rem" />}
          required
        />
        <TextInput
          label="Project ID"
          placeholder="0f8b1c2d-…"
          value={config.project_id || ''}
          onChange={(e) => updateConfig('project_id', e.currentTarget.value)}
          description="Required. The engine refuses an empty project rather than listing the whole organization."
          required
        />
      </FormRow>

      <Divider label="Authentication" labelPosition="center" />

      <Text size="xs" c="dimmed">
        Supply a username and password, or a token from a secret store. Prefer the password for a
        long-running pipeline: the source logs in again when the token expires, which a static token
        cannot do.
      </Text>

      <FormRow cols={2}>
        <TextInput
          label="Username"
          placeholder="hermod-service"
          value={config.username || ''}
          onChange={(e) => updateConfig('username', e.currentTarget.value)}
        />
        <PasswordInput
          label="Password"
          value={config.password || ''}
          onChange={(e) => updateConfig('password', e.currentTarget.value)}
        />
      </FormRow>

      <FormRow cols={2}>
        <PasswordInput
          label="Token"
          placeholder="Paste a token instead of a password"
          value={config.token || ''}
          onChange={(e) => updateConfig('token', e.currentTarget.value)}
          description="Used as-is. It is not refreshed, so an expiring token stops the source."
        />
        <TextInput
          label="Organization ID"
          placeholder="Leave blank unless the account has several"
          value={config.organization_id || ''}
          onChange={(e) => updateConfig('organization_id', e.currentTarget.value)}
        />
      </FormRow>

      <Divider label="What to read" labelPosition="center" />

      <Select
        label="Stream"
        value={stream}
        onChange={(value) => updateConfig('stream', value || 'instances')}
        data={[
          { value: 'instances', label: 'Process instances — one row per run' },
          { value: 'tasks', label: 'Human tasks — every status, not just open ones' },
          { value: 'incidents', label: 'Incidents — the failures an operator resolves' },
        ]}
        allowDeselect={false}
      />

      {stream === 'incidents' && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          The engine lists incidents per instance, not per project, so this stream finds the failed
          instances first and asks each one what went wrong. That costs one request per failed
          instance per poll — and it only sees instances still inside the scan window below. Raise
          it when instances are numerous and failures are slow to appear.
        </Alert>
      )}

      <FormRow cols={3}>
        <TextInput
          label="Poll interval"
          placeholder="10s"
          value={config.poll_interval || ''}
          onChange={(e) => updateConfig('poll_interval', e.currentTarget.value)}
          description="Blank polls every 10s."
        />
        <NumberInput
          label="Page size"
          placeholder="Server default"
          min={0}
          value={config.page_size === undefined || config.page_size === '' ? '' : Number(config.page_size)}
          onChange={(value) => updateConfig('page_size', value === '' ? '' : String(value))}
          description="Rows per listing. A page smaller than the number of rows appearing between two polls loses the oldest of them."
        />
        <NumberInput
          label="Scan pages"
          placeholder="1"
          min={0}
          value={config.scan_pages === undefined || config.scan_pages === '' ? '' : Number(config.scan_pages)}
          onChange={(value) => updateConfig('scan_pages', value === '' ? '' : String(value))}
          description="Incidents only: how many pages of instances to search for failures."
          disabled={stream !== 'incidents'}
        />
      </FormRow>

      <TextInput
        label="Timeout"
        placeholder="30s"
        value={config.timeout || ''}
        onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
        description="Bounds one call. Blank uses the client's 30s default."
      />

      <Text size="xs" c="dimmed">
        The position resumes from the last row the pipeline acknowledged, not the last one read, so
        a restart comes back for anything that was still in flight.
      </Text>
    </Stack>
  );
}
