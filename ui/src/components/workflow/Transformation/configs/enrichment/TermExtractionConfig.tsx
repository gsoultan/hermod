import { Autocomplete, NumberInput, Stack, TagsInput } from '@mantine/core';
import { ExpressionFieldPicker, TargetFieldInput } from '@/components/workflow/Transformation/expressionField';

interface TermExtractionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

export function TermExtractionConfig({ config, updateNodeConfig, nodeId, fieldPaths }: TermExtractionConfigProps) {
  return (

    <Stack gap="xs">
      <Autocomplete
        label="Source Field"
        placeholder="description"
        data={fieldPaths || []}
        value={config.field || ''}
        onChange={(val: string) => updateNodeConfig(nodeId, { field: val })}
        required
        description="Field or expression to extract terms from (e.g. description, tostring(source.id))."
        rightSection={
          <ExpressionFieldPicker
            field={config.field}
            writesTo={{ targetField: config.targetField, suffix: '_terms' }}
            onApply={(patch) => updateNodeConfig(nodeId, patch)}
          />
        }
        rightSectionPointerEvents="all"
      />
      {/* Shows what is configured. It used to show "keywords" for a node with
          no target set, while the engine wrote to <field>_terms. */}
      <TargetFieldInput
        field={config.field}
        value={config.targetField}
        suffix="_terms"
        onChange={(targetField) => updateNodeConfig(nodeId, { targetField })}
      />
      <NumberInput
        label="Min Word Length"
        value={config.minLength || 3}
        min={1}
        onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { minLength: val })}
      />
      <TagsInput
        label="Stopwords"
        placeholder="Add words to ignore"
        value={typeof config.stopWords === 'string' ? config.stopWords.split(',') : (config.stopWords || [])}
        onChange={(val: string[]) => updateNodeConfig(nodeId, { stopWords: val.join(',') })}
      />
    </Stack>
  
  );
}
