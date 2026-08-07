import { Accordion, Button, Checkbox, Group, NumberInput, PasswordInput, Select, Stack, Text, TextInput } from '@mantine/core';
import { IconDownload, IconUpload } from '@tabler/icons-react';

import type { SettingsController } from './useSettingsController';

/** The "platform" tab of Settings. State lives in useSettingsController. */
export function PlatformTab({ ctx }: { ctx: SettingsController }) {
  const {
    dbConn,
    dbType,
    fileInputRef,
    fileStorageType,
    handleExport,
    handleImport,
    handleSave,
    handleSaveStateStore,
    localDir,
    logDbConn,
    logDbType,
    message,
    notifSettings,
    s3AccessKey,
    s3Bucket,
    s3Endpoint,
    s3Region,
    s3SecretKey,
    s3UseSSL,
    saveMutation,
    saveNotifMutation,
    saveStateStoreMutation,
    saveStorageMutation,
    setDbConn,
    setDbType,
    setFileStorageType,
    setLocalDir,
    setLogDbConn,
    setLogDbType,
    setNotifSettings,
    setS3AccessKey,
    setS3Bucket,
    setS3Endpoint,
    setS3Region,
    setS3SecretKey,
    setS3UseSSL,
    setStateAddr,
    setStateDB,
    setStatePass,
    setStatePath,
    setStatePrefix,
    setStateType,
    stateAddr,
    stateDB,
    statePass,
    statePath,
    statePrefix,
    stateType,
  } = ctx;

  return (
    <>
          <Accordion variant="separated" radius="md" defaultValue={['general-settings']} multiple>
            <Accordion.Item value="general-settings">
              <Accordion.Control>General Settings</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <TextInput
                  label="Base URL"
                  placeholder="http://hermod.example.com"
                  value={notifSettings.base_url}
                  onChange={(e) => setNotifSettings({ ...notifSettings, base_url: e.target.value })}
                  description="The base URL of the Hermod UI, used for generating links in notifications."
                />
                <NumberInput
                  label="Log Retention (Days)"
                  placeholder="30"
                  min={0}
                  value={notifSettings.logs_retention_days}
                  onChange={(val) => setNotifSettings({ ...notifSettings, logs_retention_days: Number(val) })}
                  description="0 means keep logs forever. Applies globally; workflows may override."
                />
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={() => saveNotifMutation.mutate(notifSettings)} loading={saveNotifMutation.isPending}>Save General Settings</Button>
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="database-configuration">
              <Accordion.Control>Database Configuration</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <Select
                  label="Database Type"
                  placeholder="Select database type"
                  data={[
                    { value: 'sqlite', label: 'SQLite' },
                    { value: 'postgres', label: 'PostgreSQL' },
                    { value: 'mysql', label: 'MySQL' },
                    { value: 'mariadb', label: 'MariaDB' },
                    { value: 'mongodb', label: 'MongoDB' },
                  ]}
                  value={dbType}
                  onChange={setDbType}
                />
                <TextInput
                  label="Connection String"
                  placeholder={dbType === 'sqlite' ? 'hermod.db' : 'postgres://user:pass@localhost:5432/db'}
                  value={dbConn}
                  onChange={(e) => setDbConn(e.currentTarget.value)}
                />
                {message && (
                  <Text c={message.type === 'success' ? 'green' : 'red'} size="sm">
                    {message.text}
                  </Text>
                )}
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSave} loading={saveMutation.isPending}>Update Database</Button>
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="log-database-configuration">
              <Accordion.Control>Log Database Configuration</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <Select
                  label="Log Database Type"
                  placeholder="Select database type"
                  description="Choose a separate database for logs to improve performance and isolation."
                  data={[
                    { value: '', label: 'Use Primary Database' },
                    { value: 'sqlite', label: 'SQLite' },
                    { value: 'postgres', label: 'PostgreSQL' },
                    { value: 'mysql', label: 'MySQL' },
                    { value: 'mariadb', label: 'MariaDB' },
                    { value: 'mongodb', label: 'MongoDB' },
                    { value: 'pebble', label: 'Pebble (Local KV)' },
                  ]}
                  value={logDbType || ''}
                  onChange={setLogDbType}
                />
                <TextInput
                  label="Log Connection String / Path"
                  placeholder={logDbType === 'pebble' ? 'logs.db' : (logDbType === 'sqlite' ? 'hermod_logs.db' : 'postgres://...')}
                  value={logDbConn}
                  onChange={(e) => setLogDbConn(e.currentTarget.value)}
                />
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSave} loading={saveMutation.isPending}>Update Log Database</Button>
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="global-state-store">
              <Accordion.Control>Global State Store</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure a distributed state store for consistent stateful transformations across multiple worker instances.
                </Text>
                <Select
                  label="Store Type"
                  placeholder="Select type"
                  data={[
                    { value: 'sqlite', label: 'Local SQLite (Standalone)' },
                    { value: 'redis', label: 'Redis (Distributed)' },
                    { value: 'etcd', label: 'Etcd (Distributed)' },
                  ]}
                  value={stateType}
                  onChange={(val) => setStateType(val || 'sqlite')}
                />
                {stateType === 'sqlite' && (
                  <TextInput
                    label="Database Path"
                    placeholder="hermod_state.db"
                    value={statePath}
                    onChange={(e) => setStatePath(e.currentTarget.value)}
                  />
                )}
                {(stateType === 'redis' || stateType === 'etcd') && (
                  <Stack gap="xs">
                    <TextInput
                      label={stateType === 'redis' ? 'Redis Address' : 'Etcd Endpoints'}
                      placeholder={stateType === 'redis' ? 'localhost:6379' : 'localhost:2379'}
                      value={stateAddr}
                      onChange={(e) => setStateAddr(e.currentTarget.value)}
                    />
                    {stateType === 'redis' && (
                      <Group grow>
                        <PasswordInput
                          label="Password"
                          placeholder="Optional"
                          value={statePass}
                          onChange={(e) => setStatePass(e.currentTarget.value)}
                        />
                        <NumberInput
                          label="DB Index"
                          value={stateDB}
                          onChange={(val) => setStateDB(Number(val || 0))}
                        />
                      </Group>
                    )}
                    <TextInput
                      label="Key Prefix"
                      placeholder="hermod:"
                      value={statePrefix}
                      onChange={(e) => setStatePrefix(e.currentTarget.value)}
                    />
                  </Stack>
                )}
                <Group justify="flex-end">
                  <Button variant="light" color="teal" size="xs" onClick={handleSaveStateStore} loading={saveStateStoreMutation.isPending}>Update State Store</Button>
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="file-storage">
              <Accordion.Control>File Storage</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure where uploaded files (like CSVs for file sources or email templates) are stored.
                </Text>
                <Select
                  label="Storage Type"
                  placeholder="Select type"
                  data={[
                    { value: 'local', label: 'Local Filesystem' },
                    { value: 's3', label: 'S3 Compatible' },
                  ]}
                  value={fileStorageType}
                  onChange={(val) => setFileStorageType(val || 'local')}
                />
                {fileStorageType === 'local' && (
                  <TextInput
                    label="Local Directory"
                    placeholder="uploads"
                    value={localDir}
                    onChange={(e) => setLocalDir(e.currentTarget.value)}
                    description="Relative or absolute path to store files locally on the API server."
                  />
                )}
                {fileStorageType === 's3' && (
                  <Stack gap="xs">
                    <TextInput
                      label="Endpoint"
                      placeholder="https://minio.example.com"
                      value={s3Endpoint}
                      onChange={(e) => setS3Endpoint(e.currentTarget.value)}
                      description="Leave empty for AWS S3. Use URL for MinIO, Wasabi, etc."
                    />
                    <Group grow>
                      <TextInput
                        label="Region"
                        placeholder="us-east-1"
                        value={s3Region}
                        onChange={(e) => setS3Region(e.currentTarget.value)}
                      />
                      <TextInput
                        label="Bucket"
                        placeholder="hermod-uploads"
                        value={s3Bucket}
                        onChange={(e) => setS3Bucket(e.currentTarget.value)}
                      />
                    </Group>
                    <Group grow>
                      <TextInput
                        label="Access Key ID"
                        placeholder="Required"
                        value={s3AccessKey}
                        onChange={(e) => setS3AccessKey(e.currentTarget.value)}
                      />
                      <PasswordInput
                        label="Secret Access Key"
                        placeholder="Required"
                        value={s3SecretKey}
                        onChange={(e) => setS3SecretKey(e.currentTarget.value)}
                      />
                    </Group>
                    <Checkbox
                      label="Use SSL"
                      checked={s3UseSSL}
                      onChange={(e) => setS3UseSSL(e.currentTarget.checked)}
                    />
                  </Stack>
                )}
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={() => saveStorageMutation.mutate()} loading={saveStorageMutation.isPending}>Update File Storage</Button>
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="maintenance-backup">
              <Accordion.Control>Maintenance & Backup</Accordion.Control>
              <Accordion.Panel>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Export your entire configuration including Sources, Sinks, Workflows, and Transformations.
                </Text>
                <Group>
                  <Button variant="outline" size="sm" leftSection={<IconDownload size="1rem" />} onClick={handleExport}>
                    Export Config
                  </Button>
                  <Button variant="outline" color="orange" size="sm" leftSection={<IconUpload size="1rem" />} onClick={() => fileInputRef.current?.click()}>
                    Import Config
                  </Button>
                  <input
                    type="file"
                    aria-label="Import configuration file"
                    ref={fileInputRef}
                    style={{ display: 'none' }}
                    accept=".json"
                    onChange={handleImport}
                  />
                </Group>
              </Stack>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
    </>
  );
}
