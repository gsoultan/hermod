import { Autocomplete, JsonInput, NumberInput, Stack } from '@mantine/core';

interface FuzzyLookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

export function FuzzyLookupConfig({ config, updateNodeConfig, nodeId, fieldPaths }: FuzzyLookupConfigProps) {
  return (

    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="input_name"
        data={fieldPaths || []}
        value={config.field || ''}
        onChange={(val: string) => updateNodeConfig(nodeId, { field: val })}
        required
        description="Field or expression to use for matching (e.g. name, lower(source.name))."
      />
      <NumberInput
        label="Similarity Threshold (0-1)"
        value={config.threshold || 0.8}
        min={0}
        max={1}
        step={0.05}
        onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { threshold: val })}
      />
      <JsonInput
        label="Options (JSON Array)"
        placeholder='["Option 1", "Option 2"]'
        value={config.options || ''}
        onChange={(val: string) => updateNodeConfig(nodeId, { options: val })}
        minRows={5}
        formatOnBlur
      />
    </Stack>
  
  );
}
