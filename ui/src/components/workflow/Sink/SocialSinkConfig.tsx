import { PasswordInput, TextInput, Stack, Alert } from '@mantine/core';
import { IconInfoCircle } from '@tabler/icons-react';
import { FormRow } from '@/components/common/FormRow';

interface SocialSinkConfigProps {
  type: string;
  config: any;
  updateConfig: (key: string, value: any) => void;
}

/**
 * Twitter/X, Facebook, Instagram, LinkedIn and TikTok.
 *
 * One component because they are one shape — a credential and, for the three
 * that post on behalf of an account rather than a user, the id of that account.
 * The key names differ per connector (`token` for Twitter, `access_token` for
 * the rest) and are taken from `createSinkBase`.
 */
export function SocialSinkConfig({ type, config, updateConfig }: SocialSinkConfigProps) {
  // Twitter is the odd one out: its factory case reads `token`, not `access_token`.
  const tokenKey = type === 'twitter' ? 'token' : 'access_token';

  const target: Record<string, { key: string; label: string; placeholder: string; description: string }> = {
    facebook: {
      key: 'page_id',
      label: 'Page ID',
      placeholder: '123456789012345',
      description: 'The page posts are published to. The token must be a page access token for it.',
    },
    instagram: {
      key: 'ig_user_id',
      label: 'Instagram user ID',
      placeholder: '17841400000000000',
      description: 'The professional account id, not the @handle.',
    },
    linkedin: {
      key: 'person_urn',
      label: 'Author URN',
      placeholder: 'urn:li:person:AbC123',
      description: 'The member or organisation posting. LinkedIn rejects a post without one.',
    },
  };

  const second = target[type];

  return (
    <Stack gap="md">
      <FormRow cols={1}>
        <PasswordInput
          label="Access token"
          placeholder="Paste the token"
          value={config[tokenKey] || ''}
          onChange={(e) => updateConfig(tokenKey, e.currentTarget.value)}
          description="Stored encrypted. Scope it to posting only — a sink never needs to read."
          required
        />
      </FormRow>

      {second && (
        <FormRow cols={1}>
          <TextInput
            label={second.label}
            placeholder={second.placeholder}
            value={config[second.key] || ''}
            onChange={(e) => updateConfig(second.key, e.currentTarget.value)}
            description={second.description}
            required
          />
        </FormRow>
      )}

      <Alert icon={<IconInfoCircle size="1rem" />} color="gray" variant="light">
        Every message becomes a post. These platforms rate-limit hard and do not de-duplicate, so
        put a filter upstream unless the source is already low volume.
      </Alert>
    </Stack>
  );
}
