import { Badge, Button, Code, Group, Paper, PasswordInput, Select, Stack, Text, TextInput, Title } from '@mantine/core';
import { IconLock, IconShieldLock } from '@tabler/icons-react';
import { copyToClipboard, generateStrongPassword } from '@/utils/cryptoUtils';

import type { SettingsController } from './useSettingsController';

/** The "security" tab of Settings. State lives in useSettingsController. */
export function SecurityTab({ ctx }: { ctx: SettingsController }) {
  const {
    awsRegion,
    azureUrl,
    baoAddr,
    baoMount,
    baoToken,
    cryptoKey,
    envPrefix,
    handleSaveSecrets,
    saveCryptoMutation,
    saveSecretsMutation,
    secretType,
    setAwsRegion,
    setAzureUrl,
    setBaoAddr,
    setBaoMount,
    setBaoToken,
    setCryptoKey,
    setEnvPrefix,
    setSecretType,
    setVaultAddr,
    setVaultMount,
    setVaultToken,
    vaultAddr,
    vaultMount,
    vaultToken,
  } = ctx;

  return (
    <>
          <Stack gap="xl">
            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconShieldLock size="1.2rem" color="blue" />
                <Title order={4}>Enterprise Secret Management</Title>
                <Badge variant="dot" color="blue">Enterprise</Badge>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  Configure external secret managers to securely resolve sensitive configuration values (marked with <Code>secret:</Code> prefix).
                </Text>
                <Select
                  label="Manager Type"
                  placeholder="Select manager"
                  data={[
                    { value: 'env', label: 'Environment Variables (Default)' },
                    { value: 'vault', label: 'HashiCorp Vault' },
                    { value: 'openbao', label: 'OpenBao' },
                    { value: 'aws', label: 'AWS Secrets Manager' },
                    { value: 'azure', label: 'Azure Key Vault' },
                  ]}
                  value={secretType}
                  onChange={(val) => setSecretType(val || 'env')}
                />
                {secretType === 'env' && (
                  <TextInput
                    label="Environment Prefix"
                    placeholder="e.g. HERMOD_SECRET_"
                    value={envPrefix}
                    onChange={(e) => setEnvPrefix(e.currentTarget.value)}
                    description="Only env vars starting with this prefix will be searched."
                  />
                )}
                {secretType === 'vault' && (
                  <Stack gap="xs">
                    <TextInput label="Vault Address" value={vaultAddr} onChange={(e) => setVaultAddr(e.currentTarget.value)} />
                    <PasswordInput label="Vault Token" value={vaultToken} onChange={(e) => setVaultToken(e.currentTarget.value)} />
                    <TextInput label="KV Mount Path" value={vaultMount} onChange={(e) => setVaultMount(e.currentTarget.value)} />
                  </Stack>
                )}
                {secretType === 'openbao' && (
                  <Stack gap="xs">
                    <TextInput label="OpenBao Address" value={baoAddr} onChange={(e) => setBaoAddr(e.currentTarget.value)} />
                    <PasswordInput label="OpenBao Token" value={baoToken} onChange={(e) => setBaoToken(e.currentTarget.value)} />
                    <TextInput label="KV Mount Path" value={baoMount} onChange={(e) => setBaoMount(e.currentTarget.value)} />
                  </Stack>
                )}
                {secretType === 'aws' && (
                  <TextInput label="AWS Region" value={awsRegion} onChange={(e) => setAwsRegion(e.currentTarget.value)} />
                )}
                {secretType === 'azure' && (
                  <TextInput label="Key Vault URL" value={azureUrl} onChange={(e) => setAzureUrl(e.currentTarget.value)} />
                )}
                <Group justify="flex-end">
                  <Button variant="light" size="xs" onClick={handleSaveSecrets} loading={saveSecretsMutation.isPending}>Update Secret Manager</Button>
                </Group>
              </Stack>
            </Paper>

            <Paper withBorder p="md" radius="md">
              <Group gap="xs" mb="md">
                <IconLock size="1.2rem" color="red" />
                <Title order={4}>Encryption Master Key</Title>
              </Group>
              <Stack gap="md">
                <Text size="sm" c="dimmed">
                  The Master Key is used to encrypt sensitive data in the database (e.g. SMTP passwords, API tokens).
                  Rotating the key here updates the configuration and applies the new key for future encryptions.
                </Text>
                <PasswordInput
                  label="New Master Key"
                  placeholder="At least 16 characters"
                  value={cryptoKey}
                  onChange={(e) => setCryptoKey(e.currentTarget.value)}
                />
                <Group justify="space-between">
                  <Group>
                    <Button variant="light" size="xs" onClick={() => setCryptoKey(generateStrongPassword(32))}>
                      Generate Key
                    </Button>
                    <Button
                      variant="default"
                      size="xs"
                      onClick={() => cryptoKey && copyToClipboard(cryptoKey)}
                      disabled={!cryptoKey}
                    >
                      Copy
                    </Button>
                  </Group>
                  <Button 
                    variant="light" 
                    color="red" 
                    size="xs" 
                    onClick={() => saveCryptoMutation.mutate(cryptoKey)}
                    loading={saveCryptoMutation.isPending}
                    disabled={cryptoKey.length < 16}
                  >
                    Update & Rotate Key
                  </Button>
                </Group>
              </Stack>
            </Paper>
          </Stack>
    </>
  );
}
