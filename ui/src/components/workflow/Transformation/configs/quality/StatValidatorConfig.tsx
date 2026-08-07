import { Alert, Autocomplete, Grid, NumberInput, Select, Stack, Text } from '@mantine/core';
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
        <Grid>
          <Grid.Col span={6}>
            <Autocomplete
              label="Field to Validate"
              description="Numeric field to monitor for anomalies"
              placeholder="e.g. price, amount, latency"
              data={fieldPaths || []}
              value={config.field || ''}
              onChange={(val) => updateNodeConfig(nodeId, { field: val })}
            />
          </Grid.Col>
          <Grid.Col span={6}>
            <Select
              label="On Anomaly Detected"
              description="Action to take when an outlier is found"
              data={[
                { value: 'tag', label: 'Tag only (set metadata anomaly=true)' },
                { value: 'drop', label: 'Drop message (stop processing)' }
              ]}
              value={config.action || 'tag'}
              onChange={(val) => updateNodeConfig(nodeId, { action: val || 'tag' })}
            />
          </Grid.Col>
          <Grid.Col span={6}>
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
          </Grid.Col>
          <Grid.Col span={6}>
            <NumberInput
              label="Minimum Samples"
              description="Samples needed before triggering validation"
              min={1}
              value={Number(config.min_samples) || 10}
              onChange={(val) => updateNodeConfig(nodeId, { min_samples: val })}
            />
          </Grid.Col>
        </Grid>
        <Alert icon={<IconInfoCircle size="1rem" />} color="blue" variant="light">
          Uses Welford's online algorithm for stable rolling mean and standard deviation.
        </Alert>
      </Stack>
    );
}
