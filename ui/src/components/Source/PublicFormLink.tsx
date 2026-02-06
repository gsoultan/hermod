import { Card, Stack, Text, TextInput, ActionIcon, Button } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconCopy } from '@tabler/icons-react';
interface PublicFormLinkProps {
  path?: string;
}

export function PublicFormLink({ path }: PublicFormLinkProps) {
  const origin = window.location.origin;
  const publicPath = (path || '').startsWith('/api/forms/') ? (path || '').slice('/api/forms/'.length) : path;
  
  const url = publicPath ? `${origin}/forms/${publicPath}` : '';

  return (
    <Card withBorder p="md" radius="md">
      <Stack gap="xs">
        <Text size="sm" fw={600}>Public Form URL</Text>
        <TextInput 
          value={url} 
          readOnly 
          rightSection={
            <ActionIcon 
              onClick={() => { if (url) navigator.clipboard.writeText(url); notifications.show({ title: 'Copied', message: 'URL copied', color: 'blue' }); }}
              aria-label="Copy public form URL"
            >
              <IconCopy size="1rem" />
            </ActionIcon>
          }
        />
        <Button variant="light" disabled={!url} onClick={() => { if (url) window.open(url, '_blank'); }}>
          Open Public Form
        </Button>
        <Text size="xs" c="dimmed">Users can submit the form without custom headers when "Allow public submissions" is enabled.</Text>
      </Stack>
    </Card>
  );
}


