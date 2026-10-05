import { Autocomplete, NumberInput, Stack, TagsInput } from '@mantine/core';
import { ExpressionFieldPicker, TargetFieldInput } from '@/components/workflow/Transformation/expressionField';

interface TermExtractionConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

/**
 * The node's own stop words as a list of words. They are stored as
 * comma-separated text, which the node reads; a config made elsewhere may hold
 * a list. Blanks are dropped: "".split(",") is [""], which drew as one empty
 * word.
 */
function stopWordList(stopWords: unknown): string[] {
  const words = typeof stopWords === 'string' ? stopWords.split(',') : Array.isArray(stopWords) ? stopWords.map(String) : [];
  return words.map((w) => w.trim()).filter(Boolean);
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
        description="Shorter words are left out."
        // minLen is the key the node read before it read this one; a node made
        // through the API may still hold it (term_extraction.go, minWordLength).
        value={config.minLength || config.minLen || 3}
        min={1}
        onChange={(val: string | number | undefined) => updateNodeConfig(nodeId, { minLength: val })}
      />
      <TagsInput
        label="Stopwords"
        placeholder="Add words to ignore"
        description="Left out as well as the common words the node always ignores, such as the, and, of."
        value={stopWordList(config.stopWords)}
        onChange={(val: string[]) => updateNodeConfig(nodeId, { stopWords: val.join(',') })}
      />
    </Stack>
  
  );
}
