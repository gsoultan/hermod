import { Autocomplete, NumberInput, Stack, TagsInput, TextInput } from '@mantine/core';

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
      />
      <TextInput
        label="Target Field"
        placeholder="keywords"
        value={config.targetField || 'keywords'}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { targetField: e.currentTarget.value })}
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
