import { Autocomplete, JsonInput, NumberInput, Stack } from '@mantine/core';
import { ExpressionFieldPicker, TargetFieldInput } from '@/components/workflow/Transformation/expressionField';

interface FuzzyLookupConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  fieldPaths?: string[];
}

/**
 * The node's options as the text this editor edits.
 *
 * This box stores JSON text, and the node reads that. A node made through the
 * API or a bundle holds a list instead, which a text input would show as
 * "Jakarta,Bandung" and write back as that.
 */
function optionsText(options: unknown): string {
  if (typeof options === 'string') return options;
  return options == null ? '' : JSON.stringify(options, null, 2);
}

/**
 * What the node will say about these options, before a record has to: it
 * refuses text that is there but is not a JSON list (fuzzy_lookup.go,
 * fuzzyOptions). That text used to count as no options, so the node passed
 * every record through unmatched.
 */
function optionsError(text: string): string | undefined {
  if (text.trim() === '') return undefined;
  try {
    if (Array.isArray(JSON.parse(text))) return undefined;
  } catch {
    // Not JSON at all; the message below covers it.
  }
  return 'Options must be a JSON list, such as ["Jakarta", "Bandung"]. The node fails every record until they are.';
}

export function FuzzyLookupConfig({ config, updateNodeConfig, nodeId, fieldPaths }: FuzzyLookupConfigProps) {
  const options = optionsText(config.options);

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
        description="The values the field is matched against. The closest one above the threshold is written to the target field."
        value={options}
        error={optionsError(options)}
        // JsonInput shows only a red border for text that is not JSON, unless
        // it is given the words for it.
        validationError={optionsError(options)}
        onChange={(val: string) => updateNodeConfig(nodeId, { options: val })}
        minRows={5}
        formatOnBlur
      />
    </Stack>
  
  );
}
