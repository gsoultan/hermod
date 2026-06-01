import { TextInput } from '@mantine/core'
import type { FC } from 'react'

export type MilvusSinkConfigProps = {
  config: any
  updateConfig: (key: string, value: any) => void
}

export const MilvusSinkConfig: FC<MilvusSinkConfigProps> = ({
  config,
  updateConfig,
}) => {
  return (
    <>
      <TextInput 
        label="Address" 
        placeholder="localhost:19530" 
        value={config.address || ''} 
        onChange={(e) => updateConfig('address', e.target.value)} 
        required 
      />
      <TextInput 
        label="Collection Name" 
        placeholder="my_collection" 
        value={config.collection_name || ''} 
        onChange={(e) => updateConfig('collection_name', e.target.value)} 
        required 
        mt="sm" 
      />
      <TextInput 
        label="Partition Name" 
        placeholder="_default" 
        value={config.partition_name || ''} 
        onChange={(e) => updateConfig('partition_name', e.target.value)} 
        mt="sm" 
      />
      <TextInput 
        label="Vector Column" 
        placeholder="embeddings" 
        value={config.vector_column || ''} 
        onChange={(e) => updateConfig('vector_column', e.target.value)} 
        required 
        mt="sm" 
      />
      <TextInput 
        label="ID Column" 
        placeholder="id" 
        value={config.id_column || ''} 
        onChange={(e) => updateConfig('id_column', e.target.value)} 
        required 
        mt="sm" 
      />
      <TextInput 
        label="Username" 
        placeholder="root" 
        value={config.username || ''} 
        onChange={(e) => updateConfig('username', e.target.value)} 
        mt="sm" 
      />
      <TextInput 
        label="Password" 
        type="password" 
        placeholder="milvus" 
        value={config.password || ''} 
        onChange={(e) => updateConfig('password', e.target.value)} 
        mt="sm" 
      />
    </>
  )
}
