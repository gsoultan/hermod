import { Alert, Card, Group, NumberInput, Stack, TagsInput, Text, ThemeIcon, rem } from '@mantine/core';
import { IconChecklist, IconInfoCircle } from '@tabler/icons-react';

interface DQScorerConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

/**
 * Editor for the Data Quality Scorer node.
 *
 * The node has been offered in the palette since the palette was written, but
 * had no editor at all — dropping it produced a node that could not be
 * configured. Keys mirror the backend transformer
 * (pkg/comm/transformer/advanced/dq_scorer.go): `required_fields` and
 * `min_score`.
 */
export function DQScorerConfig({ config, updateNodeConfig, nodeId, fieldPaths }: DQScorerConfigProps) {
  const requiredFields: string[] = Array.isArray(config.required_fields)
    ? config.required_fields
    : typeof config.required_fields === 'string' && config.required_fields
      ? config.required_fields.split(',').map((f: string) => f.trim()).filter(Boolean)
      : [];

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size={rem(18)} />} color="orange" variant="light" radius="md" title="Data Quality Scoring">
        <Text size="sm">
          Scores each message on completeness and writes the result to <code>_dq_score</code> in the
          payload and <code>dq_score</code> in metadata. Messages scoring below the minimum are
          rejected and follow the node&apos;s error branch.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="orange" radius="md">
              <IconChecklist size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>Scoring rules</Text>
          </Group>

          <TagsInput
            label="Required fields"
            description="Each missing field lowers the score. Leave empty to score on presence of any data."
            placeholder="Type a field path and press Enter"
            data={fieldPaths || []}
            value={requiredFields}
            onChange={(vals) => updateNodeConfig(nodeId, { required_fields: vals })}
            clearable
          />

          <NumberInput
            label="Minimum score"
            description="Messages scoring below this are rejected. 0 disables the threshold."
            placeholder="0"
            min={0}
            max={100}
            step={5}
            value={config.min_score ?? 0}
            onChange={(val) => updateNodeConfig(nodeId, { min_score: typeof val === 'number' ? val : 0 })}
          />
        </Stack>
      </Card>
    </Stack>
  );
}
