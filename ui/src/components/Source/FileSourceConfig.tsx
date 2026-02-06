import { TextInput, Stack, Group, Tabs, FileButton, Button, Divider, Checkbox, PasswordInput, NumberInput, Fieldset, Select, Text } from '@mantine/core';import { useState } from 'react';import { IconCloud, IconFileImport, IconLink, IconServer, IconUpload } from '@tabler/icons-react';
interface FileSourceConfigProps {
  config: Record<string, any>;
  updateConfig: (key: string, value: any) => void;
  handleFileUpload?: (file: File | null) => void;
  uploading?: boolean;
}

export function FileSourceConfig({ config, updateConfig, handleFileUpload, uploading }: FileSourceConfigProps) {
  const [activeTab, setActiveTab] = useState<string | null>(config.source_type || 'local');

  const handleTabChange = (value: string | null) => {
    setActiveTab(value);
    updateConfig('source_type', value || 'local');
  };

  return (
    <Stack gap="md">
      <Tabs value={activeTab} onChange={handleTabChange} variant="outline" radius="md">
        <Tabs.List grow>
          <Tabs.Tab value="local" leftSection={<IconFileImport size="1rem" />}>Local / Network</Tabs.Tab>
          <Tabs.Tab value="http" leftSection={<IconLink size="1rem" />}>HTTP / HTTPS</Tabs.Tab>
          <Tabs.Tab value="s3" leftSection={<IconCloud size="1rem" />}>S3 Compatible</Tabs.Tab>
          <Tabs.Tab value="ftp" leftSection={<IconServer size="1rem" />}>FTP / SFTP</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="local" pt="md">
          <Fieldset legend="Local Path Settings">
            <Stack gap="sm">
              <TextInput 
                label="File or Directory Path" 
                placeholder="/path/to/data" 
                value={config.local_path || config.file_path || ''} 
                onChange={(e) => updateConfig('local_path', e.target.value)} 
                description="Absolute path to a file or a folder on the worker machine."
                required 
              />
              <Group grow>
                <TextInput 
                  label="Filename Pattern" 
                  placeholder="*.csv, *.pdf, data-*.json" 
                  value={config.pattern || ''} 
                  onChange={(e) => updateConfig('pattern', e.target.value)} 
                  description="Filter files by name (Glob pattern)"
                />
                <Checkbox 
                  label="Recursive Scan" 
                  checked={config.recursive === 'true'} 
                  onChange={(e) => updateConfig('recursive', e.target.checked ? 'true' : 'false')} 
                  description="Scan subdirectories"
                  mt="xl"
                />
              </Group>
              {handleFileUpload && (
                <Group justify="center" mt="sm">
                  <FileButton onChange={handleFileUpload} accept="*/*">
                    {(props) => (
                      <Button {...props} variant="light" leftSection={<IconUpload size="1rem" />} loading={uploading}>
                        Upload File to Server
                      </Button>
                    )}
                  </FileButton>
                </Group>
              )}
            </Stack>
          </Fieldset>
        </Tabs.Panel>

        <Tabs.Panel value="http" pt="md">
          <Fieldset legend="HTTP Endpoint Settings">
            <Stack gap="sm">
              <TextInput 
                label="URL" 
                placeholder="https://example.com/api/data" 
                value={config.url || ''} 
                onChange={(e) => updateConfig('url', e.target.value)} 
                required 
              />
              <TextInput 
                label="Headers" 
                placeholder="Authorization: Bearer token, X-Custom: value" 
                value={config.headers || ''} 
                onChange={(e) => updateConfig('headers', e.target.value)} 
                description="Comma-separated key-value pairs"
              />
            </Stack>
          </Fieldset>
        </Tabs.Panel>

        <Tabs.Panel value="s3" pt="md">
          <Fieldset legend="S3 Storage Settings">
            <Stack gap="sm">
              <Group grow>
                <TextInput label="Region" placeholder="us-east-1" value={config.s3_region || ''} onChange={(e) => updateConfig('s3_region', e.target.value)} />
                <TextInput label="Bucket" placeholder="my-bucket" value={config.s3_bucket || ''} onChange={(e) => updateConfig('s3_bucket', e.target.value)} required />
              </Group>
              <TextInput 
                label="Key Prefix" 
                placeholder="data/incoming/" 
                value={config.s3_key || ''} 
                onChange={(e) => updateConfig('s3_key', e.target.value)} 
                description="Folder path or specific file key in the bucket"
              />
              <TextInput 
                label="Endpoint URL" 
                placeholder="https://s3.amazonaws.com" 
                value={config.s3_endpoint || ''} 
                onChange={(e) => updateConfig('s3_endpoint', e.target.value)} 
                description="Optional: Custom endpoint for MinIO, DigitalOcean Spaces, etc."
              />
              <Group grow>
                <TextInput label="Access Key" value={config.s3_access_key || ''} onChange={(e) => updateConfig('s3_access_key', e.target.value)} />
                <PasswordInput label="Secret Key" value={config.s3_secret_key || ''} onChange={(e) => updateConfig('s3_secret_key', e.target.value)} />
              </Group>
            </Stack>
          </Fieldset>
        </Tabs.Panel>

        <Tabs.Panel value="ftp" pt="md">
          <Fieldset legend="FTP / SFTP Settings">
            <Stack gap="sm">
              <Group grow>
                <TextInput label="Host" placeholder="ftp.example.com" value={config.ftp_host || ''} onChange={(e) => updateConfig('ftp_host', e.target.value)} required />
                <NumberInput label="Port" placeholder="21" value={config.ftp_port ? parseInt(config.ftp_port) : 21} onChange={(val) => updateConfig('ftp_port', val.toString())} />
              </Group>
              <Group grow>
                <TextInput label="Username" placeholder="ftpuser" value={config.ftp_user || ''} onChange={(e) => updateConfig('ftp_user', e.target.value)} />
                <PasswordInput label="Password" value={config.ftp_password || ''} onChange={(e) => updateConfig('ftp_password', e.target.value)} />
              </Group>
              <TextInput 
                label="Remote Directory" 
                placeholder="/uploads" 
                value={config.ftp_root || ''} 
                onChange={(e) => updateConfig('ftp_root', e.target.value)} 
                description="Starting directory on the FTP server"
              />
              <TextInput 
                label="Filename Pattern" 
                placeholder="*.csv" 
                value={config.pattern || ''} 
                onChange={(e) => updateConfig('pattern', e.target.value)} 
              />
            </Stack>
          </Fieldset>
        </Tabs.Panel>
      </Tabs>

      <Divider label="Ingestion & Format" labelPosition="center" />
      
      <Group grow align="flex-start">
        <Stack gap="xs">
          <Select 
            label="Data Format" 
            data={[
              { value: 'raw', label: 'Raw Bytes (Single message per file)' },
              { value: 'csv', label: 'CSV (Row-by-row streaming)' }
            ]} 
            value={config.format || 'raw'} 
            onChange={(val: string | null) => updateConfig('format', val || 'raw')} 
          />
          <Text size="xs" c="dimmed">
            {config.format === 'csv' 
              ? 'Files will be parsed line by line. Each row becomes a separate Hermod message.' 
              : 'The entire file content will be sent as the message payload.'}
          </Text>
        </Stack>
        <TextInput 
          label="Poll Interval" 
          placeholder="5m" 
          value={config.poll_interval || ''} 
          onChange={(e) => updateConfig('poll_interval', e.target.value)} 
          description="E.g. 30s, 5m, 1h. Leave empty for one-time scan."
        />
      </Group>

      {config.format === 'csv' && (
        <Fieldset legend="CSV Parsing Options">
          <Group grow>
            <TextInput 
              label="Delimiter" 
              placeholder="," 
              value={config.delimiter || ','} 
              onChange={(e) => updateConfig('delimiter', e.target.value)} 
              maxLength={1} 
            />
            <Checkbox 
              label="First row is header" 
              checked={config.has_header !== 'false'} 
              onChange={(e) => updateConfig('has_header', e.target.checked ? 'true' : 'false')} 
              mt="xl"
            />
          </Group>
        </Fieldset>
      )}
    </Stack>
  );
}


