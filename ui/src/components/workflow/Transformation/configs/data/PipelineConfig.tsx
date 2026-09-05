import { Stack, Text, JsonInput } from '@mantine/core';
import { useMemo } from 'react';

interface PipelineConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

/**
 * What is wrong with a steps document, or null if nothing is.
 *
 * The pane stores the raw string the user types — the right call, it
 * round-trips their formatting — but it used to store anything at all,
 * silently. A broken document sat in the node config with nothing on screen
 * to say the pipeline would not run.
 */
export function describeStepsProblem(raw: string): string | null {
  if (!raw.trim()) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return 'Not valid JSON yet, so this pipeline will not run until it is.';
  }
  if (!Array.isArray(parsed)) {
    return 'Must be a JSON array of steps, for example [{"transType": "mask", "field": "email"}].';
  }
  return null;
}

export function PipelineConfig({ config, updateNodeConfig, nodeId }: PipelineConfigProps) {
  // `??`, not `||`: with `||` an emptied pane was coerced straight back to
  // "[]", so the field could never actually be cleared.
  const raw: string = config.steps ?? '[]';
  const problem = useMemo(() => describeStepsProblem(raw), [raw]);

  return (
    <Stack gap="xs" style={{ flex: 1 }}>
      <Text size="sm" fw={500}>Steps</Text>
      <JsonInput
        label="Steps (JSON Array)"
        placeholder='[{"transType": "mask", "field": "email", "maskType": "email"}, {"transType": "set", "column.processed": true}]'
        value={raw}
        onChange={(val) => updateNodeConfig(nodeId, { steps: val })}
        error={problem}
        // Deliberately no formatOnBlur: it rewrites the text under the caret,
        // and the draft is the user's to format.
        minRows={20}
        styles={{
          root: { flex: 1, display: 'flex', flexDirection: 'column' },
          wrapper: { flex: 1, display: 'flex', flexDirection: 'column' },
          input: { flex: 1, fontFamily: 'monospace', fontSize: 'var(--mantine-font-size-xs)' },
        }}
        description="List of transformation steps to execute in order."
      />
    </Stack>
  );
}
