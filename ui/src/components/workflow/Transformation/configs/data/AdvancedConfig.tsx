import { ColumnFieldsEditor } from '../../fieldMappings/ColumnFieldsEditor';

interface AdvancedConfigProps {
  config: any;
  updateNodeConfig: (id: string, config: any, replace?: boolean) => void;
  nodeId: string;
  availableFields: any[];
  transType: string;
  addField: () => void;
}

export function AdvancedConfig({ config, updateNodeConfig, nodeId, availableFields, transType, addField }: AdvancedConfigProps) {
  return (
    <ColumnFieldsEditor
      config={config}
      nodeId={nodeId}
      updateNodeConfig={updateNodeConfig}
      availableFields={availableFields}
      transType={transType}
      addField={addField}
      title="Transformation Rules"
      // advanced.go clears the message before writing its rules.
      summary="Builds a new record from these rules. Fields no rule writes are dropped."
      jsonLabel="Config (JSON)"
      jsonPlaceholder='{"column.user.name": "lower(source.user.name)"}'
    />
  );
}
