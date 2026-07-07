import {
  ActionIcon,
  Button,
  Group,
  Stack,
  Text,
  TextInput,
  Alert,
  Divider,
  Card,
  Badge,
  rem,
  Box,
  Tooltip,
  Paper,
} from '@mantine/core';
import { FilterEditor } from './FilterEditor';
import {
  IconInfoCircle,
  IconPlus,
  IconRoute,
  IconTrash,
  IconSettings,
  IconGitBranch,
} from '@tabler/icons-react';

interface RouterEditorProps {
  selectedNode: any;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  availableFields: any[];
}

export function RouterEditor({
  selectedNode,
  updateNodeConfig,
  availableFields = [],
}: RouterEditorProps) {
  let rules: any[] = [];
  try {
    rules =
      typeof selectedNode.data.rules === 'string'
        ? JSON.parse(selectedNode.data.rules || '[]')
        : selectedNode.data.rules || [];
  } catch (e) {
    rules = [];
  }

  const updateRules = (next: any[]) => {
    updateNodeConfig(selectedNode.id, { rules: JSON.stringify(next) });
  };

  const addRule = () => {
    const next = [...rules, { label: `path_${rules.length + 1}`, conditions: [] }];
    updateRules(next);
  };

  const removeRule = (index: number) => {
    const next = [...rules];
    next.splice(index, 1);
    updateRules(next);
  };

  const updateRuleLabel = (index: number, label: string) => {
    const next = [...rules];
    next[index] = { ...next[index], label };
    updateRules(next);
  };

  const updateRuleConditions = (index: number, conditions: any[]) => {
    const next = [...rules];
    next[index] = { ...next[index], conditions };
    updateRules(next);
  };

  return (
    <Stack gap="lg">
      <Alert
        variant="light"
        color="blue"
        title="Content-Based Router"
        icon={<IconInfoCircle size={rem(18)} />}
        radius="md"
      >
        Rules are evaluated in order. The first rule that matches will determine the branch. If no
        rules match, the message follows the "default" branch.
      </Alert>

      <Stack gap="md">
        {rules.map((rule, idx) => (
          <Card key={idx} withBorder radius="md" p="md" className="hover:shadow-sm transition-shadow">
            <Stack gap="md">
              <Group justify="space-between">
                <Group gap="xs">
                  <IconGitBranch size={rem(18)} className="text-indigo-500" />
                  <Text size="sm" fw={700}>
                    BRANCH #{idx + 1}
                  </Text>
                  {rule.label && (
                    <Badge color="indigo" variant="light" size="xs">
                      {rule.label}
                    </Badge>
                  )}
                </Group>
                <Tooltip label="Delete branch">
                  <ActionIcon color="red" variant="subtle" onClick={() => removeRule(idx)}>
                    <IconTrash size={rem(18)} />
                  </ActionIcon>
                </Tooltip>
              </Group>

              <TextInput
                label="Branch Label"
                placeholder="e.g. high_priority"
                value={rule.label || ''}
                onChange={(e) => updateRuleLabel(idx, e.target.value)}
                description="Identifier for the outgoing edge in the workflow designer."
                size="sm"
                required
              />

              <Divider
                label={
                  <Group gap={4}>
                    <IconSettings size={rem(14)} />
                    <Text size="xs" fw={600}>
                      ROUTING CONDITIONS
                    </Text>
                  </Group>
                }
                labelPosition="center"
              />

              <Box bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))" p="xs">
                <FilterEditor
                  conditions={rule.conditions || []}
                  availableFields={availableFields}
                  onChange={(conds) => updateRuleConditions(idx, conds)}
                />
              </Box>
            </Stack>
          </Card>
        ))}
      </Stack>

      <Button
        variant="light"
        color="indigo"
        fullWidth
        leftSection={<IconPlus size={rem(18)} />}
        onClick={addRule}
      >
        Add Routing Rule
      </Button>

      <Paper withBorder p="md" radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))">
        <Group gap="xs">
          <IconRoute size={rem(20)} className="text-gray-400" />
          <Box>
            <Text size="sm" fw={600}>
              Default Branch
            </Text>
            <Text size="xs" c="dimmed">
              Fallback path when no conditions above are met.
            </Text>
          </Box>
        </Group>
      </Paper>
    </Stack>
  );
}


