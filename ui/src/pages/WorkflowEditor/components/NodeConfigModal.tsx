import { Modal, ScrollArea } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { SourceForm } from '../../../components/SourceForm';
import { SinkForm } from '../../../components/SinkForm';
import { TransformationForm } from '../../../components/TransformationForm';

type NodeConfigModalProps = {
  opened: boolean;
  onClose: () => void;
  selectedNode: any | null;
  updateNodeConfig: (nodeId: string, config: any, replace?: boolean) => void;
  onSave?: (data: any) => void;
  vhost?: string;
  workerID?: string;
  // Optional context to enrich TransformationForm
  availableFields?: string[];
  incomingPayload?: any;
  sources?: any[];
  sinks?: any[];
  sinkSchema?: any;
  onRefreshFields?: () => void;
  isRefreshing?: boolean;
};

export function NodeConfigModal({ 
  opened, onClose, selectedNode, updateNodeConfig, onSave, vhost, workerID, 
  availableFields = [], incomingPayload, sources = [], sinks = [], sinkSchema,
  onRefreshFields, isRefreshing
}: NodeConfigModalProps) {
  if (!selectedNode) return null;

  const type = selectedNode.type;
  const isEditing = selectedNode.data?.ref_id !== 'new';
  
  const sourceData = type === 'source' ? (sources.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;
  const sinkData = type === 'sink' ? (sinks.find(s => s.id === selectedNode.data.ref_id) || selectedNode.data) : null;

  const title = (() => {
    switch (type) {
      case 'source': return `Configure Source: ${selectedNode.data?.label || ''}`;
      case 'sink': return `Configure Sink: ${selectedNode.data?.label || ''}`;
      case 'transformation': return `Configure Transformation: ${selectedNode.data?.label || ''}`;
      case 'validator': return `Configure Validator: ${selectedNode.data?.label || ''}`;
      default: return 'Configure Node';
    }
  })();

  const handleSave = (cfg: any) => {
    if (!selectedNode) return;
    if (onSave) {
      onSave(cfg);
    } else {
      updateNodeConfig(selectedNode.id, cfg);
    }
    notifications.show({ message: 'Configuration saved.', color: 'green' });
    onClose();
  };

  return (
    <Modal opened={opened} onClose={onClose} title={title} fullScreen padding="md" withinPortal>
      <ScrollArea h="calc(100vh - 120px)" offsetScrollbars>
        {type === 'source' && (
          <SourceForm 
            initialData={sourceData} 
            onSave={handleSave} 
            vhost={vhost} 
            workerID={workerID}
            embedded
            isEditing={isEditing}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
            onRunSimulation={onRefreshFields} // Use refresh as simulation for source
          />
        )}
        {type === 'sink' && (
          <SinkForm 
            initialData={sinkData} 
            onSave={handleSave} 
            vhost={vhost} 
            workerID={workerID}
            availableFields={availableFields}
            incomingPayload={incomingPayload}
            embedded
            isEditing={isEditing}
            sinks={sinks}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
          />
        )}
        {(type === 'transformation' || type === 'validator') && (
          <TransformationForm 
            selectedNode={selectedNode} 
            updateNodeConfig={updateNodeConfig} 
            availableFields={availableFields} 
            incomingPayload={incomingPayload}
            sources={sources}
            sinkSchema={sinkSchema}
            onRefreshFields={onRefreshFields}
            isRefreshing={isRefreshing}
          />
        )}
      </ScrollArea>
    </Modal>
  );
}

export default NodeConfigModal;
