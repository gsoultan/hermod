import { Button, Group, Menu, Text, ActionIcon, Tooltip } from '@mantine/core';import { IconBolt, IconLayoutList, IconPlus, IconShieldLock, IconSparkles, IconTrash } from '@tabler/icons-react';
interface QuickActionsProps {
  onApplyTemplate: (template: string) => void;
}

export function QuickActions({ onApplyTemplate }: QuickActionsProps) {
  return (
    <Group justify="space-between" mb="xs">
      <Text size="xs" fw={700} c="dimmed">QUICK ACTIONS</Text>
      <Group gap={5}>
        <Menu shadow="md" width={200} position="bottom-end">
          <Menu.Target>
            <Button 
              size="compact-xs" 
              variant="light" 
              color="grape" 
              leftSection={<IconSparkles size="0.8rem" />}
            >
              Presets
            </Button>
          </Menu.Target>

          <Menu.Dropdown>
            <Menu.Label>Security & Privacy</Menu.Label>
            <Menu.Item 
              leftSection={<IconShieldLock size="0.8rem" />} 
              onClick={() => onApplyTemplate('pii_masking')}
            >
              Mask All PII
            </Menu.Item>
            <Menu.Item 
              leftSection={<IconShieldLock size="0.8rem" />} 
              onClick={() => onApplyTemplate('mask_emails')}
            >
              Mask Emails
            </Menu.Item>

            <Menu.Divider />
            
            <Menu.Label>Structure</Menu.Label>
            <Menu.Item 
              leftSection={<IconLayoutList size="0.8rem" />} 
              onClick={() => onApplyTemplate('flatten')}
            >
              Flatten Object
            </Menu.Item>
            <Menu.Item 
              leftSection={<IconPlus size="0.8rem" />} 
              onClick={() => onApplyTemplate('audit_fields')}
            >
              Add Audit Fields
            </Menu.Item>

            <Menu.Divider />
            
            <Menu.Label>Clean Up</Menu.Label>
            <Menu.Item 
              color="red" 
              leftSection={<IconTrash size="0.8rem" />} 
              onClick={() => onApplyTemplate('clear')}
            >
              Clear All Logic
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>

        <Tooltip label="Auto-generate logic using AI (Coming Soon)">
           <ActionIcon variant="light" color="blue" size="sm" disabled>
             <IconBolt size="0.8rem" />
           </ActionIcon>
        </Tooltip>
      </Group>
    </Group>
  );
}


