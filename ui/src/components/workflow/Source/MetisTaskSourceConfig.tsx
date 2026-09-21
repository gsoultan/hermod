import {
  TextInput,
  PasswordInput,
  NumberInput,
  Stack,
  Divider,
  Alert,
  Text,
} from '@mantine/core';
import { IconSitemap, IconInfoCircle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface MetisTaskSourceConfigProps {
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * The metis external-task source runs one step of a BPMN process as a Hermod
 * pipeline: it locks the work the engine published, the workflow does it, and
 * acknowledging the message completes the task with whatever the pipeline
 * produced.
 *
 * Keys match `createSourceBase` case "metis_task" and the `ExternalTaskConfig`
 * in `pkg/comm/source/metis`.
 */
export function MetisTaskSourceConfig({ config, updateConfig }: MetisTaskSourceConfigProps) {
  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
        Mark a service task with this topic on the diagram and the engine publishes it as work
        instead of calling out. Whatever this workflow's sinks produce is sent back as the step's
        output variables when the message is acknowledged, and the process carries on from there.
      </Alert>

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
          label="Topic"
          placeholder="reverse-charge"
          value={config.topic || ''}
          onChange={(e) => updateConfig('topic', e.currentTarget.value)}
          description="The service task's topic attribute on the diagram. A topic is subscribed to across the organization, so one workflow can serve the same step in several processes."
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

      <Divider label="Locking" labelPosition="center" />

      <Text size="xs" c="dimmed">
        A fetched task is locked to this worker until the lock lapses. Acknowledging after that is
        refused, because the task may already belong to somebody else — so the lock has to outlast
        everything the pipeline does with a task, including the slowest sink.
      </Text>

      <FormRow cols={3}>
        <TextInput
          label="Lock duration"
          placeholder="1m"
          value={config.lock_duration || ''}
          onChange={(e) => updateConfig('lock_duration', e.currentTarget.value)}
          description="Blank locks for 1m."
        />
        <NumberInput
          label="Max tasks per fetch"
          placeholder="5"
          min={1}
          value={config.max_tasks === undefined || config.max_tasks === '' ? '' : Number(config.max_tasks)}
          onChange={(value) => updateConfig('max_tasks', value === '' ? '' : String(value))}
          description="Every task in a batch is locked when the batch is fetched, so the last one's lock runs while the others are still in the pipeline."
        />
        <TextInput
          label="Poll interval"
          placeholder="2s"
          value={config.poll_interval || ''}
          onChange={(e) => updateConfig('poll_interval', e.currentTarget.value)}
          description="The pause after a fetch that found nothing, or failed. A fetch that found work is followed immediately by the next one. Blank waits 2s."
        />
      </FormRow>

      <TextInput
        label="Worker ID"
        placeholder="Generated per source — leave blank"
        value={config.worker_id || ''}
        onChange={(e) => updateConfig('worker_id', e.currentTarget.value)}
        description="The identity holding the locks. The engine authorises a completion by this alone, so two pipelines sharing one can finish each other's tasks. Blank generates a distinct id, which is the safe answer."
      />

      <Divider label="What goes back to the process" labelPosition="center" />

      <TextInput
        label="Output variables"
        placeholder="reversed, reference"
        value={config.variable_fields || ''}
        onChange={(e) => updateConfig('variable_fields', e.currentTarget.value)}
        description="Comma-separated field names to send back as the step's output. Blank sends every field, which also sends back whatever the step was given — name them to keep something the process should not learn out of the instance."
      />

      <TextInput
        label="Timeout"
        placeholder="30s"
        value={config.timeout || ''}
        onChange={(e) => updateConfig('timeout', e.currentTarget.value)}
        description="Bounds one call. Blank uses the client's 30s default."
      />

      <Text size="xs" c="dimmed">
        A task the pipeline cannot finish is never completed, so its lock lapses and the engine
        hands it to the next worker. Nothing is persisted here — redelivery is the engine's job —
        which means a task may be run twice, so the sinks downstream want to be idempotent.
      </Text>
    </Stack>
  );
}
