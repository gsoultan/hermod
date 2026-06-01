import { Fieldset, Stack, TextInput, NumberInput, Tabs, Group, PasswordInput, FileButton, Button } from '@mantine/core';
import { useState } from 'react';
import { IconCloud, IconFileImport, IconLink, IconUpload } from '@tabler/icons-react';

interface ExcelSourceConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  handleFileUpload?: (file: File | null) => void;
  uploading?: boolean;
}

export function ExcelSourceConfig({ config, updateConfig, handleFileUpload, uploading }: ExcelSourceConfigProps) {
  const [activeTab, setActiveTab] = useState<string | null>(config.source_type || 'local');

  const handleTabChange = (value: string | null) => {
    setActiveTab(value);
    updateConfig('source_type', value || 'local');
  };

  return (
    <Fieldset legend="Excel (.xlsx) Settings">
      <Stack gap="sm">
        <Tabs value={activeTab} onChange={handleTabChange} variant="outline" radius="md">
          <Tabs.List grow>
            <Tabs.Tab value="local" leftSection={<IconFileImport size="1rem" />}>Local / Network</Tabs.Tab>
            <Tabs.Tab value="http" leftSection={<IconLink size="1rem" />}>HTTP / HTTPS</Tabs.Tab>
            <Tabs.Tab value="s3" leftSection={<IconCloud size="1rem" />}>S3 Compatible</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="local" pt="md">
            <Stack gap="sm">
              <TextInput 
                label="Base Path" 
                placeholder="./uploads" 
                value={config.base_path || config.local_path || ''}
                onChange={(e) => updateConfig('base_path', e.target.value)}
                description="Directory containing Excel files on the worker machine"
                required
              />
              <Group grow>
                <TextInput 
                  label="Filename Pattern" 
                  placeholder="*.xlsx" 
                  value={config.pattern || ''}
                  onChange={(e) => updateConfig('pattern', e.target.value)}
                  description="Glob pattern relative to Base Path"
                />
              </Group>
              {handleFileUpload && (
                <Group justify="center" mt="sm">
                  <FileButton onChange={handleFileUpload} accept=".xlsx">
                    {(props) => (
                      <Button {...props} variant="light" leftSection={<IconUpload size="1rem" />} loading={uploading}>
                        Upload Excel to Server
                      </Button>
                    )}
                  </FileButton>
                </Group>
              )}
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="http" pt="md">
            <Stack gap="sm">
              <TextInput 
                label="URL" 
                placeholder="https://example.com/data.xlsx" 
                value={config.url || ''}
                onChange={(e) => updateConfig('url', e.target.value)}
                required
              />
              <TextInput 
                label="Headers" 
                placeholder="Authorization: Bearer token, X-Custom: value" 
                value={config.headers || ''} 
                onChange={(e) => updateConfig('headers', e.target.value)} 
                description="Comma-separated key:value pairs"
              />
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="s3" pt="md">
            <Stack gap="sm">
              <Group grow>
                <TextInput label="Region" placeholder="us-east-1" value={config.s3_region || ''} onChange={(e) => updateConfig('s3_region', e.target.value)} />
                <TextInput label="Bucket" placeholder="my-bucket" value={config.s3_bucket || ''} onChange={(e) => updateConfig('s3_bucket', e.target.value)} required />
              </Group>
              <TextInput 
                label="Key Prefix / Key" 
                placeholder="data/incoming/ or path/to/file.xlsx" 
                value={config.s3_key || ''} 
                onChange={(e) => updateConfig('s3_key', e.target.value)} 
                description="Folder prefix or a specific file key"
              />
              <TextInput 
                label="Endpoint URL" 
                placeholder="https://s3.amazonaws.com" 
                value={config.s3_endpoint || ''} 
                onChange={(e) => updateConfig('s3_endpoint', e.target.value)} 
                description="Optional: MinIO / custom S3 endpoint"
              />
              <Group grow>
                <TextInput label="Access Key" value={config.s3_access_key || ''} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
                <PasswordInput label="Secret Key" value={config.s3_secret_key || ''} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
              </Group>
              <TextInput 
                label="Filename Pattern (optional)" 
                placeholder="*.xlsx" 
                value={config.pattern || ''}
                onChange={(e) => updateConfig('pattern', e.target.value)}
                description="Filter keys by glob on basename"
              />
            </Stack>
          </Tabs.Panel>
        </Tabs>

        <TextInput 
          label="Sheet Name" 
          placeholder="(first sheet by default)" 
          value={config.sheet || ''}
          onChange={(e) => updateConfig('sheet', e.target.value)}
        />
        <NumberInput 
          label="Header Row (0 = none)" 
          placeholder="1" 
          value={config.header_row ? parseInt(config.header_row) : 1}
          onChange={(val) => updateConfig('header_row', (val ?? 0).toString())}
        />
        <NumberInput 
          label="Start Row (0 = auto)" 
          placeholder="2" 
          value={config.start_row ? parseInt(config.start_row) : 0}
          onChange={(val) => updateConfig('start_row', (val ?? 0).toString())}
        />
      </Stack>
    </Fieldset>
  );
}
