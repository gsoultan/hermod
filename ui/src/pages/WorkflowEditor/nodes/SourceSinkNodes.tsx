import { Position } from 'reactflow';
import { 
  IconWorld, IconSettingsAutomation, IconFileSpreadsheet, 
  IconCircles, IconDatabase, IconCloudUpload, IconMail, IconDeviceFloppy,
  IconMessage, IconTerminal2
} from '@tabler/icons-react';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';

export const SourceNode = ({ id, data }: any) => {
  const getIcon = () => {
    if (data.type === 'webhook') return IconWorld;
    if (data.type === 'cron') return IconSettingsAutomation;
    if (data.type === 'csv') return IconFileSpreadsheet;
    if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis'].includes(data.type)) return IconCircles;
    return IconDatabase;
  };

  return (
    <BaseNode id={id} type="Source" color="blue" icon={getIcon()} data={data}>
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="blue" />
    </BaseNode>
  );
};

export const SinkNode = ({ id, data }: any) => {
  const getIcon = () => {
    if (['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'mongodb', 'cassandra', 'sqlite', 'clickhouse', 'yugabyte'].includes(data.type)) return IconDatabase;
    if (data.type === 'api' || data.type === 'http') return IconCloudUpload;
    if (data.type === 'smtp') return IconMail;
    if (data.type === 'telegram' || data.type === 'fcm') return IconMessage;
    if (data.type === 'file') return IconDeviceFloppy;
    if (data.type === 'stdout') return IconTerminal2;
    if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'pubsub', 'kinesis', 'pulsar'].includes(data.type)) return IconCircles;
    return IconDatabase;
  };

  return (
    <BaseNode id={id} type="Sink" color="green" icon={getIcon()} data={data}>
      <TargetHandle position={Position.Left} color="green" />
    </BaseNode>
  );
};
