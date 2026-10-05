import { ActionIcon, Button, Code, Collapse, CopyButton, Group, List, Stack, Text, Title, Tooltip } from '@mantine/core';
import { IconCheck, IconChevronDown, IconChevronRight, IconCopy } from '@tabler/icons-react';
import { useState } from 'react';
import { GRPC_EXAMPLE_PATH, GRPC_SOURCE_PROTO, SAMPLE_PAYLOAD_JSON, grpcPublishCommand } from './grpcContract';

interface GrpcCallGuideProps {
  path?: string;
  hasApiKey: boolean;
  /** The source responds synchronously: the reply carries the workflow's result. */
  waits?: boolean;
}

function CopyIcon({ value, label }: { value: string; label: string }) {
  return (
    <CopyButton value={value}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? 'Copied' : 'Copy'}>
          <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} size="sm" onClick={copy} aria-label={label}>
            {copied ? <IconCheck size="0.9rem" /> : <IconCopy size="0.9rem" />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  );
}

/**
 * GrpcCallGuide answers what the gRPC source's one field cannot: what a client
 * has to send, where, and what it gets back. Hermod serves a fixed service, so
 * the contract is shown here rather than uploaded.
 */
export function GrpcCallGuide({ path, hasApiKey, waits = false }: GrpcCallGuideProps) {
  const [contractOpen, setContractOpen] = useState(false);
  const command = grpcPublishCommand(path?.trim() || GRPC_EXAMPLE_PATH, hasApiKey);

  return (
    <Stack gap="xs">
      <Title order={5}>Calling this source</Title>
      <List size="sm" withPadding spacing={4}>
        <List.Item>
          Hermod serves one fixed service, <Code>SourceService.Publish</Code>. There is no .proto to upload: generate
          your client from the contract below and send your record as JSON in <Code>payload</Code>.
        </List.Item>
        <List.Item>
          Send the path above, exactly as written, in <Code>PublishRequest.path</Code>. It is a label, not a URL.
        </List.Item>
        <List.Item>
          Connect to this server&apos;s gRPC port: 50051 unless it was started with <Code>--grpc-port</Code> or{' '}
          <Code>HERMOD_GRPC_PORT</Code>. The port is plaintext.
        </List.Item>
        <List.Item>The source receives only while its workflow is running.</List.Item>
        {waits ? (
          <List.Item>
            The call waits for the workflow. The reply carries its <Code>status</Code>, an <Code>error</Code> when the
            record failed, and the final <Code>record</Code>.
          </List.Item>
        ) : (
          <List.Item>
            The reply <Code>dispatched</Code> means the record was queued. It does not wait for transformations or
            sinks.
          </List.Item>
        )}
      </List>

      <Group justify="space-between" align="center" mt={4}>
        <Text size="sm" fw={500}>Try it</Text>
        <CopyIcon value={command} label="Copy the sample command" />
      </Group>
      <Code block data-testid="grpc-sample-command">{command}</Code>
      <Text size="xs" c="dimmed">
        <Code>payload</Code> is a bytes field, so a JSON tool needs it base64-encoded: the value above is{' '}
        <Code>{SAMPLE_PAYLOAD_JSON}</Code>. A generated client passes the JSON text as it is.
      </Text>

      <Group justify="space-between" align="center">
        <Button
          variant="subtle"
          size="compact-sm"
          px={0}
          leftSection={contractOpen ? <IconChevronDown size="1rem" /> : <IconChevronRight size="1rem" />}
          aria-expanded={contractOpen}
          onClick={() => setContractOpen((open) => !open)}
        >
          The contract (source.proto)
        </Button>
        <CopyIcon value={GRPC_SOURCE_PROTO} label="Copy source.proto" />
      </Group>
      <Collapse expanded={contractOpen}>
        <Code block data-testid="grpc-contract">{GRPC_SOURCE_PROTO}</Code>
      </Collapse>
    </Stack>
  );
}
