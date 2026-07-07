import {
  Stack,
  Autocomplete,
  Select,
  Alert,
  Text,
  Card,
  Group,
  rem,
  ThemeIcon,
} from '@mantine/core';
import { useMemo } from 'react';
import { IconInfoCircle, IconEyeOff, IconTag, IconAdjustmentsHorizontal } from '@tabler/icons-react';

interface MaskConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
  availableFields: any[];
}

export function MaskConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
}: MaskConfigProps) {
  const fieldPaths = useMemo(() => 
    (availableFields || []).map(f => typeof f === 'string' ? f : f.path),
    [availableFields]
  );

  return (
    <Stack gap="md">
      <Alert
        icon={<IconInfoCircle size={rem(18)} />}
        color="violet"
        variant="light"
        radius="md"
        title="Data Masking"
      >
        <Text size="sm">
          Protect sensitive information by obfuscating data like emails, names, or credit card
          numbers.
        </Text>
      </Alert>

      <Card withBorder radius="md" p="md">
        <Stack gap="md">
          <Group gap="xs">
            <ThemeIcon variant="light" color="violet" radius="md">
              <IconEyeOff size={rem(18)} />
            </ThemeIcon>
            <Text size="sm" fw={600}>
              Masking Rules
            </Text>
          </Group>

          <Autocomplete
            label="Field Path"
            placeholder="e.g. user.email, lower(source.email) (use * for all)"
            data={fieldPaths || []}
            value={config.field || ''}
            onChange={(val) => updateNodeConfig(nodeId, { field: val })}
            description="Field or expression to mask. Use * to scan all top-level fields for PII."
            size="sm"
            leftSection={<IconTag size={rem(16)} />}
          />

          <Select
            label="Masking Strategy"
            data={[
              { label: 'All characters (****)', value: 'all' },
              { label: 'Partial mask (ab****yz)', value: 'partial' },
              { label: 'Email pattern (a****@b.com)', value: 'email' },
              { label: 'Smart PII Detection', value: 'pii' },
            ]}
            value={config.maskType || 'all'}
            onChange={(val) => updateNodeConfig(nodeId, { maskType: val || 'all' })}
            size="sm"
            leftSection={<IconAdjustmentsHorizontal size={rem(16)} />}
            description="Select how the sensitive value should be obscured."
          />
        </Stack>
      </Card>
    </Stack>
  );
}
