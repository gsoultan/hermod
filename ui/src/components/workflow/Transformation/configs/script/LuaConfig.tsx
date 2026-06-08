import { Code, Textarea, Alert, Text, Stack, Card, Group, rem, Box } from '@mantine/core';
import { IconInfoCircle, IconTerminal2, IconScript } from '@tabler/icons-react';

interface LuaConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function LuaConfig({ config, updateNodeConfig, nodeId }: LuaConfigProps) {
  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="blue"
        variant="light"
        radius="md"
        title="Lua Scripting"
      >
        <Text size="sm">
          Write custom logic using Lua. The script must define a <code>transform(msg)</code> function
          that returns the modified message object.
        </Text>
      </Alert>

      <Card withBorder radius="md" p={0} style={{ overflow: 'hidden' }}>
         <Box p="xs" bg="var(--mantine-color-gray-1)" className="dark:bg-dark-8" style={{ borderBottom: '1px solid var(--mantine-color-gray-3)' }}>
            <Group gap="xs">
               <IconTerminal2 size={rem(16)} className="text-blue-500" />
               <Text size="xs" fw={700} tt="uppercase">Script Editor</Text>
            </Group>
         </Box>
         <Textarea
            placeholder="function transform(msg) ... end"
            value={config.script || ''}
            onChange={(e: any) => updateNodeConfig(nodeId, { script: e.target.value })}
            minRows={20}
            autosize
            styles={{
               input: {
                  fontFamily: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
                  fontSize: rem(13),
                  border: 'none',
                  borderRadius: 0,
                  backgroundColor: 'transparent',
                  padding: rem(16),
               }
            }}
         />
      </Card>

      <Card withBorder radius="md" p="md">
         <Stack gap="xs">
            <Group gap="xs">
               <IconScript size={rem(18)} className="text-gray-500" />
               <Text size="sm" fw={600}>Example Template</Text>
            </Group>
            <Code block color="blue.1" variant="outline">
{`-- Lua Script Example
function transform(msg)
  -- Add a new field
  msg.data["processed_at"] = os.date("!%Y-%m-%dT%H:%M:%SZ")
  
  -- Access existing fields
  local status = msg.data["status"]
  if status == "ERROR" then
     msg.metadata["priority"] = "high"
  end
  
  return msg
end`}
            </Code>
         </Stack>
      </Card>
    </Stack>
  );
}
