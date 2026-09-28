import { ColumnFieldsEditor } from '../../fieldMappings/ColumnFieldsEditor';

interface SetFieldsConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  nodeId: string;
  availableFields: any[];
  addField: (path?: string, value?: string) => void;
}

export function SetFieldsConfig({
  config,
  updateNodeConfig,
  nodeId,
  availableFields,
  addField,
}: SetFieldsConfigProps) {
  return (
    <ColumnFieldsEditor
      config={config}
      nodeId={nodeId}
      updateNodeConfig={updateNodeConfig}
      availableFields={availableFields}
      transType="set"
      addField={addField}
      title="Field Mappings"
      summary="Each row sets one field in the outgoing message, adding it or overwriting it."
      jsonLabel="Fields (JSON)"
      jsonPlaceholder='{"column.user.role": "admin", "column.status": 1}'
    />
  );
}
