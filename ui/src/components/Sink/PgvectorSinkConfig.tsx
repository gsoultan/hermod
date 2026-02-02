import { TextInput } from '@mantine/core'
import type { FC } from 'react'
import { PostgresSinkConfig, type PostgresSinkConfigProps } from './PostgresSinkConfig'

export type PgvectorSinkConfigProps = Omit<PostgresSinkConfigProps, 'type'>

export const PgvectorSinkConfig: FC<PgvectorSinkConfigProps> = (props) => {
  const { config, updateConfig } = props
  return (
    <>
      <PostgresSinkConfig {...props} type="postgres" />
      <TextInput
        label="Vector Column"
        placeholder="embedding"
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
        label="Metadata Column"
        placeholder="metadata"
        value={config.metadata_column || ''}
        onChange={(e) => updateConfig('metadata_column', e.target.value)}
        description="Column to store message data as JSONB"
        mt="sm"
      />
    </>
  )
}
