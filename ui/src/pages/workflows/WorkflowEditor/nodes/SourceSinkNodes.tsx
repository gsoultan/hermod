import { Position } from '@xyflow/react';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { memo } from 'react';
import { Badge } from '@mantine/core';
import { transactionOf } from '@/utils/commitTogether';
import { IconArrowsJoin2, IconBrandDiscord, IconBrandFacebook, IconBrandInstagram, IconBrandLinkedin, IconBrandSlack, IconBrandTiktok, IconBrandTwitter, IconCircles, IconCloudUpload, IconDatabase, IconDeviceFloppy, IconFileSpreadsheet, IconMail, IconMessage, IconSettingsAutomation, IconTerminal2, IconWorld } from '@tabler/icons-react';
const SourceNodeImpl = ({ id, data, selected }: any) => {
  const getIcon = () => {
    if (data.type === 'webhook' || data.type === 'form' || data.type === 'graphql') return IconWorld;
    if (data.type === 'chat') return IconMessage;
    if (data.type === 'cron') return IconSettingsAutomation;
    if (data.type === 'csv' || data.type === 'googlesheets') return IconFileSpreadsheet;
    if (data.type === 'grpc') return IconTerminal2;
    if (data.type === 'discord') return IconBrandDiscord;
    if (data.type === 'slack') return IconBrandSlack;
    if (data.type === 'twitter') return IconBrandTwitter;
    if (data.type === 'facebook') return IconBrandFacebook;
    if (data.type === 'instagram') return IconBrandInstagram;
    if (data.type === 'linkedin') return IconBrandLinkedin;
    if (data.type === 'tiktok') return IconBrandTiktok;
    if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis'].includes(data.type)) return IconCircles;
    return IconDatabase;
  };

  return (
    <BaseNode id={id} type="Source" color="blue" icon={getIcon()} data={data} selected={selected}>
      <PlusHandle type="source" position={Position.Right} nodeId={id} color="blue" />
    </BaseNode>
  );
};

const SinkNodeImpl = ({ id, data, selected }: any) => {
  const getIcon = () => {
    if (['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'mongodb', 'cassandra', 'sqlite', 'clickhouse', 'yugabyte'].includes(data.type)) return IconDatabase;
    if (data.type === 'api' || data.type === 'http') return IconCloudUpload;
    if (data.type === 'smtp') return IconMail;
    if (data.type === 'telegram' || data.type === 'fcm') return IconMessage;
    if (data.type === 'discord') return IconBrandDiscord;
    if (data.type === 'slack') return IconBrandSlack;
    if (data.type === 'twitter') return IconBrandTwitter;
    if (data.type === 'facebook') return IconBrandFacebook;
    if (data.type === 'instagram') return IconBrandInstagram;
    if (data.type === 'linkedin') return IconBrandLinkedin;
    if (data.type === 'tiktok') return IconBrandTiktok;
    if (data.type === 'file') return IconDeviceFloppy;
    if (data.type === 'stdout') return IconTerminal2;
    if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'redis_set', 'pubsub', 'kinesis', 'pulsar'].includes(data.type)) return IconCircles;
    if (data.type === 's3' || data.type === 's3-parquet' || data.type === 'ftp') return IconCloudUpload;
    return IconDatabase;
  };

  // A sink node that commits together with others says so where the other
  // members are seen: on the canvas, by the transaction's name.
  const transaction = transactionOf(data);
  const badge = transaction ? (
    <Badge
      size="xs"
      variant="light"
      color="teal"
      radius="sm"
      leftSection={<IconArrowsJoin2 size="0.7rem" />}
      aria-label={`Commits together in transaction ${transaction}`}
      style={{ textTransform: 'none' }}
    >
      {transaction}
    </Badge>
  ) : undefined;

  return (
    <BaseNode id={id} type="Sink" color="green" icon={getIcon()} data={data} selected={selected} badge={badge}>
      <TargetHandle position={Position.Left} color="green" />
    </BaseNode>
  );
};

/*
 * Node components are memoised.
 *
 * React Flow re-renders the node layer whenever anything in it changes; without
 * memo, every node on the canvas re-rendered whenever any one node's telemetry
 * arrived — several times a second on a running workflow, each render rebuilding
 * a Paper/Group/Stack/ThemeIcon/Badge tree and its handles. React Flow's own
 * documentation requires this.
 */
export const SourceNode = memo(SourceNodeImpl);
export const SinkNode = memo(SinkNodeImpl);
SourceNode.displayName = 'SourceNode';
SinkNode.displayName = 'SinkNode';
