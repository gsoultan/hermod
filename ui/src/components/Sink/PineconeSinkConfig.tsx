import { TextInput } from '@mantine/core'
import type { FC } from 'react'

export type PineconeSinkConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
}

export const PineconeSinkConfig: FC<PineconeSinkConfigProps> = ({
  config,
  updateConfig,
}) => {
  return (
    <>
      <TextInput 
        label="API Key" 
        type="password" 
        placeholder="Pinecone API Key" 
        value={config.api_key || ''} 
        onChange={(e) => updateConfig('api_key', e.target.value)} 
        required 
      />
      <TextInput 
        label="Environment" 
        placeholder="us-west1-gcp" 
        value={config.environment || ''} 
        onChange={(e) => updateConfig('environment', e.target.value)} 
        required 
        mt="sm" 
      />
      <TextInput 
        label="Index Name" 
        placeholder="my-index" 
        value={config.index_name || ''} 
        onChange={(e) => updateConfig('index_name', e.target.value)} 
        required 
        mt="sm" 
      />
      <TextInput 
        label="Namespace" 
        placeholder="default" 
        value={config.namespace || ''} 
        onChange={(e) => updateConfig('namespace', e.target.value)} 
        mt="sm" 
      />
    </>
  )
}
