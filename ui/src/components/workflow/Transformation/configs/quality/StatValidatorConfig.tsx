import { Alert, Autocomplete, NumberInput, Select, Stack, Text } from '@mantine/core';
import { FormRow } from '@/components/common/FormRow';
import { IconInfoCircle } from '@tabler/icons-react';

interface StatValidatorConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

export function StatValidatorConfig({ config, updateNodeConfig, nodeId, fieldPaths }: StatValidatorConfigProps) {
    return (
      <Stack gap="sm">
        <Text size="sm" fw={500}>Statistical Validation Settings</Text>
        <FormRow>
          <Autocomplete
            label="Field to Validate"
            description="Numeric field to monitor for anomalies"
            placeholder="e.g. price, amount, latency"
            data={fieldPaths || []}
            value={config.field || ''}
            onChange={(val) => updateNodeConfig(nodeId, { field: val })}
          />
          <Select
            label="On Anomaly Detected"
            description="Tagging sets metadata anomaly=true and lets the message through"
            data={[
              { value: 'tag', label: 'Tag it' },
              { value: 'drop', label: 'Drop the message' }
            ]}
            value={config.action || 'tag'}
            onChange={(val) => updateNodeConfig(nodeId, { action: val || 'tag' })}
          />
        </FormRow>
        <FormRow>
          <NumberInput
            label="Z-Score Threshold"
            description="Number of standard deviations from mean"
            min={1}
            max={10}
            step={0.1}
            decimalScale={1}
            value={Number(config.threshold) || 3.0}
            onChange={(val) => updateNodeConfig(nodeId, { threshold: val })}
          />
          <NumberInput
            label="Minimum Samples"
            description="Samples needed before triggering validation"
            min={1}
            value={Number(config.min_samples) || 10}
            onChange={(val) => updateNodeConfig(nodeId, { min_samples: val })}
          />
        </FormRow>
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          Uses Welford's online algorithm for stable rolling mean and standard deviation.
        </Alert>
      </Stack>
    );
}
