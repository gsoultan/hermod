import { Position } from '@xyflow/react';
import { Button, Stack } from '@mantine/core';
import { BaseNode, PlusHandle, TargetHandle } from './BaseNode';
import { useWorkflowStore, type WorkflowState } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore';
import { useShallow } from 'zustand/react/shallow';
import { useParams } from '@tanstack/react-router';
import { apiFetch } from '../../../../api';
import { notifications } from '@mantine/notifications';
import { useState } from 'react';
import { IconArrowsExchange, IconBrandDiscord, IconBrandFacebook, IconBrandInstagram, IconBrandLinkedin, IconBrandSlack, IconBrandTiktok, IconBrandTwitter, IconCircles, IconCloudUpload, IconDatabase, IconDeviceFloppy, IconFileSpreadsheet, IconMail, IconMessage, IconSearch, IconSettingsAutomation, IconTerminal2, IconWorld } from '@tabler/icons-react';
export const SourceNode = ({ id, data, selected }: any) => {
  const getIcon = () => {
    if (data.type === 'webhook' || data.type === 'form' || data.type === 'graphql') return IconWorld;
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

export const SinkNode = ({ id, data, selected }: any) => {
  const { id: workflowId } = useParams({ strict: false }) as any;
  const { active, setDlqInspectorSink, setDlqInspectorOpened } = useWorkflowStore(useShallow((state: WorkflowState) => ({
    active: state.active,
    setDlqInspectorSink: state.setDlqInspectorSink,
    setDlqInspectorOpened: state.setDlqInspectorOpened,
  })));
  const [isDraining, setIsDraining] = useState(false);

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
    if (['kafka', 'nats', 'rabbitmq', 'rabbitmq_queue', 'redis', 'pubsub', 'kinesis', 'pulsar'].includes(data.type)) return IconCircles;
    if (data.type === 's3' || data.type === 's3-parquet' || data.type === 'ftp') return IconCloudUpload;
    return IconDatabase;
  };

  const handleInspect = (e: any) => {
    e.stopPropagation();
    setDlqInspectorSink({ id: data.ref_id, type: data.type, name: data.label, config: data });
    setDlqInspectorOpened(true);
  };

  const handleDrain = async (e: any) => {
    e.stopPropagation();
    if (!workflowId || workflowId === 'new') return;
    setIsDraining(true);
    try {
      const res = await apiFetch(`/api/workflows/${workflowId}/drain`, { method: 'POST' });
      if (res.ok) {
        notifications.show({ title: 'DLQ Draining', message: 'The engine will now prioritize messages from this sink.', color: 'blue' });
      } else {
        const err = await res.json();
        notifications.show({ title: 'Drain Failed', message: err.error || 'Failed to trigger drain', color: 'red' });
      }
    } catch (err: any) {
      notifications.show({ title: 'Error', message: err.message, color: 'red' });
    } finally {
      setIsDraining(false);
    }
  };

  return (
    <BaseNode id={id} type="Sink" color="green" icon={getIcon()} data={data} selected={selected}>
      <TargetHandle position={Position.Left} color="green" />
      {data.isDLQ && (
        <Stack gap="xs" mt="xs">
          <Button 
            variant="light" 
            color="orange" 
            size="compact-xs" 
            fullWidth 
            leftSection={<IconSearch size="0.7rem" />}
            onClick={handleInspect}
          >
            Inspect DLQ
          </Button>
          <Button 
            variant="outline" 
            color="blue" 
            size="compact-xs" 
            fullWidth 
            leftSection={<IconArrowsExchange size="0.7rem" />}
            onClick={handleDrain}
            loading={isDraining}
            disabled={!active}
          >
            Drain Now
          </Button>
        </Stack>
      )}
    </BaseNode>
  );
};


