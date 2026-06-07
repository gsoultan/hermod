import { Stack, NumberInput, Alert, Text } from '@mantine/core';
import { IconShieldLock } from '@tabler/icons-react';

interface CircuitBreakerConfigProps {
  config: any;
  nodeId: string;
  updateNodeConfig: (nodeId: string, config: any) => void;
}

export function CircuitBreakerConfig({ config, nodeId, updateNodeConfig }: CircuitBreakerConfigProps) {
  const data = config || {};

  return (
    <Stack gap="xs">
      <Alert icon={<IconShieldLock size="1rem" />} color="red" title="Circuit Breaker">
        Automatically stops the flow if failures exceed the threshold.
      </Alert>
      <NumberInput
        label="Failure Threshold"
        value={data.failure_threshold || 5}
        onChange={(val) => updateNodeConfig(nodeId, { failure_threshold: val })}
        min={1}
        description="Number of consecutive failures before opening the circuit."
      />
      <Text size="xs" c="dimmed">
        When the circuit is OPEN, messages are routed to the 'failure' branch. 
        After 30 seconds, it moves to HALF_OPEN to test recovery.
      </Text>
    </Stack>
  );
}
