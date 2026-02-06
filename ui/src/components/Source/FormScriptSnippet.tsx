import { Card, Stack, Group, Text, Code, Button } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconBraces } from '@tabler/icons-react';
interface FormScriptSnippetProps {
  path?: string;
}

export function FormScriptSnippet({ path }: FormScriptSnippetProps) {
  const origin = window.location.origin;
  const publicPath = (path || '').startsWith('/api/forms/') ? (path || '').slice('/api/forms/'.length) : path;
  
  const scriptUrl = publicPath ? `${origin}/api/forms/${publicPath}/script.js` : '';
  const scriptTag = `<script src="${scriptUrl}" async defer></script>`;

  return (
    <Card withBorder p="md" radius="md" bg="var(--mantine-color-blue-0)">
      <Stack gap="xs">
        <Group gap="xs">
          <IconBraces size="1.2rem" color="var(--mantine-color-blue-6)" />
          <Text size="sm" fw={600}>Form Script Snippet</Text>
        </Group>
        <Text size="xs" c="dimmed">
          Add this script to your website to capture form submissions automatically. 
          Ensure your form has <Code>{`data-hermod-form="${publicPath}"`}</Code> or <Code>{`id="hermod-${publicPath?.replace(/\//g, '-')}"`}</Code>.
        </Text>
        <Code block style={{ fontSize: '11px' }}>{scriptTag}</Code>
        <Button 
          variant="light" 
          size="xs" 
          disabled={!scriptUrl}
          onClick={() => {
            if (scriptTag) {
              navigator.clipboard.writeText(scriptTag);
              notifications.show({ title: 'Copied', message: 'Script tag copied to clipboard', color: 'blue' });
            }
          }}
        >
          Copy Snippet
        </Button>
      </Stack>
    </Card>
  );
}


