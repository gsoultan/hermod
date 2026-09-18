import { TextInput, NumberInput, Switch, Stack, Alert, Text } from '@mantine/core';
import { IconCircles, IconInfoCircle } from '@tabler/icons-react';

interface ForeachConfigProps {
  config: any;
  updateNodeConfig: (nodeId: string, config: any) => void;
  nodeId: string;
  /**
   * Two different nodes answer to "foreach" and this editor serves both:
   * the `foreach` node type splits the message (one per item, each carrying
   * `_item`/`_index`), while a `transformation` with transType foreach/fanout
   * keeps one message and materialises the expanded array on it. They share a
   * name and an Array Path and nothing else.
   */
  nodeType?: string;
}

export function ForeachConfig({ config, updateNodeConfig, nodeId, nodeType }: ForeachConfigProps) {
  const splitsMessages = nodeType === 'foreach';

  return (
    <Stack gap="md">
      {(!config.arrayPath || String(config.arrayPath).trim() === '') && (
        <Alert icon={<IconInfoCircle size="1rem" />} color="red">
          <Text size="sm">Array Path is required to perform fan-out. Please provide a valid path.</Text>
        </Alert>
      )}
      <Alert icon={<IconInfoCircle size="1rem" />} color="indigo">
        {splitsMessages ? (
          <Text size="sm">
            Execution-level Fan-out: Splits one message into multiple independent messages based on an array field.
            Downstream nodes will be executed once for each item.
          </Text>
        ) : (
          <Text size="sm">
            Expands an array onto the same message. Downstream nodes still run once, reading the expanded list
            from the result field. To run them once per item instead, use the Foreach (Fan-out) node
            under Logic &amp; Flow.
          </Text>
        )}
      </Alert>
      <TextInput
        label="Array Path"
        placeholder="e.g. results, data.items"
        description="Path to the array field in the message data"
        value={config.arrayPath || ''}
        onChange={(e) => updateNodeConfig(nodeId, { arrayPath: e.currentTarget.value })}
        leftSection={<IconCircles size="1rem" />}
        required
      />
      {splitsMessages ? (
        <>
          <NumberInput
            label="Max Items"
            placeholder="10000"
            description="How many messages one message may become. A larger array fails the node rather than fanning out part of it."
            min={1}
            value={config.maxItems ?? ''}
            onChange={(v) => updateNodeConfig(nodeId, { maxItems: v === '' ? '' : String(v) })}
          />
          <Switch
            label="Carry the source array on every message"
            description="Off: each message carries its own item only. On: every message also carries the whole array, which costs memory in proportion to its size squared."
            checked={config.keepSourceArray === true || config.keepSourceArray === 'true'}
            onChange={(e) => updateNodeConfig(nodeId, { keepSourceArray: e.currentTarget.checked })}
          />
          <Text size="xs" c="dimmed">
            Each message will have <code>_item</code> and <code>_index</code> fields added.
          </Text>
        </>
      ) : (
        <>
          <TextInput
            label="Result Field"
            placeholder="_fanout"
            description="Where the expanded array is written on the message"
            value={config.resultField || ''}
            onChange={(e) => updateNodeConfig(nodeId, { resultField: e.currentTarget.value })}
          />
          <TextInput
            label="Item Path"
            placeholder="e.g. id"
            description="Optional: extract this path out of each item instead of keeping the whole object"
            value={config.itemPath || ''}
            onChange={(e) => updateNodeConfig(nodeId, { itemPath: e.currentTarget.value })}
          />
          <TextInput
            label="Index Field"
            placeholder="e.g. position"
            description="Optional: write each item's position into this field on the item"
            value={config.indexField || ''}
            onChange={(e) => updateNodeConfig(nodeId, { indexField: e.currentTarget.value })}
          />
          <NumberInput
            label="Limit"
            placeholder="No limit"
            description="Optional: keep at most this many items"
            min={1}
            value={config.limit ?? ''}
            onChange={(v) => updateNodeConfig(nodeId, { limit: v === '' ? '' : String(v) })}
          />
          <Switch
            label="Drop message when the array is empty"
            description="Off: the message continues with an empty result array"
            checked={config.dropEmpty === true || config.dropEmpty === 'true'}
            onChange={(e) => updateNodeConfig(nodeId, { dropEmpty: e.currentTarget.checked })}
          />
          <Text size="xs" c="dimmed">
            The expanded array lands under <code>{config.resultField || '_fanout'}</code>.
          </Text>
        </>
      )}
    </Stack>
  );
}
