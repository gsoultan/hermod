import { Autocomplete, JsonInput, NumberInput, Stack } from '@mantine/core';
import { ExpressionFieldPicker, TargetFieldInput } from '@/components/workflow/Transformation/expressionField';

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
        rightSection={
          <ExpressionFieldPicker
            field={config.field}
            writesTo={{ targetField: config.targetField, suffix: '_fuzzy' }}
            onApply={(patch) => updateNodeConfig(nodeId, patch)}
          />
        }
        rightSectionPointerEvents="all"
      />
      {/* The node has always read targetField; there was no input for it, so
          a match could only go to <field>_fuzzy. */}
      <TargetFieldInput
        field={config.field}
        value={config.targetField}
        suffix="_fuzzy"
        onChange={(targetField) => updateNodeConfig(nodeId, { targetField })}
        description="Where the best match is written. Its score goes beside it."
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
