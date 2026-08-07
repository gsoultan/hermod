import { PasswordInput, Select, Stack, TextInput, Textarea } from '@mantine/core';

interface AIConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any) => void;
  nodeId: string;
}

export function AIConfig({ config, updateNodeConfig, nodeId }: AIConfigProps) {
  return (

    <Stack gap="md">
      <Select
        label="Provider"
        data={[
          { value: 'openai', label: 'OpenAI' },
          { value: 'ollama', label: 'Ollama (Local)' },
        ]}
        value={config.provider || 'openai'}
        onChange={(val: string | null) => updateNodeConfig(nodeId, { provider: val || 'openai' })}
      />
      <TextInput
        label="Model"
        placeholder={config.provider === 'ollama' ? 'llama3' : 'gpt-4o'}
        value={config.model || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { model: e.target.value })}
      />
      <Textarea
        label="Prompt Template"
        placeholder="Extract the sentiment and primary topic from: {{message}}"
        value={config.prompt || ''}
        onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => updateNodeConfig(nodeId, { prompt: e.target.value })}
        minRows={4}
        description="Use {{message}} to refer to the entire message, or {{field}} for specific fields."
      />
      <TextInput
        label="Target Field"
        placeholder="ai_analysis"
        value={config.targetField || ''}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { targetField: e.target.value })}
      />
      {config.provider === 'openai' && (
        <PasswordInput
          label="API Key"
          placeholder="sk-..."
          value={config.apiKey || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { apiKey: e.target.value })}
        />
      )}
      {config.provider === 'ollama' && (
        <TextInput
          label="Ollama Host"
          placeholder="http://localhost:11434"
          value={config.host || ''}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => updateNodeConfig(nodeId, { host: e.target.value })}
        />
      )}
    </Stack>
  
  );
}
