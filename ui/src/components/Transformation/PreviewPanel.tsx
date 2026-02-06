import { Alert, Badge, Button, Card, Code, Divider, Group, ScrollArea, Stack, Text, SegmentedControl, ActionIcon, Tooltip as MantineTooltip, Box } from '@mantine/core';
import { useEffect, useRef, useState } from 'react';
import { IconAlertCircle, IconCheck, IconCopy, IconEye, IconGitCompare, IconPlayerPlay } from '@tabler/icons-react';
interface PreviewPanelProps {
  title?: string;
  loading?: boolean;
  error?: string | null;
  result?: unknown;
  original?: unknown;
  onRun?: () => void;
}

export function PreviewPanel({ title = 'Preview', loading, error, result, original, onRun }: PreviewPanelProps) {
  const [viewMode, setViewMode] = useState<'transformed' | 'original' | 'diff'>('transformed');
  const [copied, setCopied] = useState(false);

  const isArray = Array.isArray(result);

  // Web Worker for JSON diff
  const workerRef = useRef<Worker | null>(null);
  const seqRef = useRef(0);
  const latestSeqRef = useRef(0);
  const [diffData, setDiffData] = useState<any | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);

  useEffect(() => {
    // Instantiate lazily on first need
    return () => {
      if (workerRef.current) {
        workerRef.current.terminate();
        workerRef.current = null;
      }
    };
  }, []);

  useEffect(() => {
    // Precompute diff whenever inputs change and original exists
    if (!original) {
      setDiffData(null);
      setDiffLoading(false);
      return;
    }
    try {
      if (!workerRef.current) {
        workerRef.current = new Worker(new URL('../../workers/diffWorker.ts', import.meta.url), { type: 'module' });
        // Attach error channels once on creation
        workerRef.current.onerror = () => {
          try {
            const o: any = original as any;
            const r: any = result as any;
            let d: any = {};
            if (o && typeof o === 'object' && r && typeof r === 'object') {
              d = {};
              for (const k of Object.keys(r)) {
                const a = o[k];
                const b = r[k];
                if (JSON.stringify(a) !== JSON.stringify(b)) d[k] = b;
              }
            } else {
              d = r;
            }
            setDiffData(d);
          } catch {
            setDiffData(result as any);
          } finally {
            setDiffLoading(false);
          }
        };
        workerRef.current.onmessageerror = () => {
          try {
            const o: any = original as any;
            const r: any = result as any;
            let d: any = {};
            if (o && typeof o === 'object' && r && typeof r === 'object') {
              d = {};
              for (const k of Object.keys(r)) {
                const a = o[k];
                const b = r[k];
                if (JSON.stringify(a) !== JSON.stringify(b)) d[k] = b;
              }
            } else {
              d = r;
            }
            setDiffData(d);
          } catch {
            setDiffData(result as any);
          } finally {
            setDiffLoading(false);
          }
        };
      }
      const id = ++seqRef.current;
      latestSeqRef.current = id;
      setDiffLoading(true);
      workerRef.current.onmessage = (ev: MessageEvent<{ id: number; result: any }>) => {
        if (ev.data?.id === latestSeqRef.current) {
          setDiffData(ev.data.result);
          setDiffLoading(false);
        }
      };
      workerRef.current.postMessage({ id, original, transformed: result });
    } catch (_err) {
      // Fallback: compute quickly on main thread (small data recommended)
      setDiffData(simpleDiff(original as any, result as any));
      setDiffLoading(false);
    }
  }, [original, result]);

  const simpleDiff = (orig: any, trans: any): any => {
    if (orig === trans) return {};
    if (!orig || typeof orig !== 'object' || !trans || typeof trans !== 'object') return trans;
    const d: any = {};
    for (const k of Object.keys(trans)) {
      const a = (orig as any)[k];
      const b = (trans as any)[k];
      if (JSON.stringify(a) !== JSON.stringify(b)) d[k] = b;
    }
    return d;
  };

  const displayData = viewMode === 'original' ? original : (viewMode === 'diff' ? (diffLoading ? { _status: 'computing diff…' } : diffData) : result);

  const copyToClipboard = () => {
    navigator.clipboard.writeText(JSON.stringify(displayData, null, 2));
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <Card withBorder shadow="sm" radius="md" p="md" h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
      <Stack h="100%" gap="xs">
        <Group justify="space-between" align="center">
          <Group gap="xs">
            <IconEye size="1.2rem" color="var(--mantine-color-blue-7)" />
            <Text size="xs" fw={700} c="dimmed">{title}</Text>
            {loading && <Badge color="blue" variant="light" size="xs">Running</Badge>}
          </Group>
          <Button size="compact-xs" variant="light" leftSection={<IconPlayerPlay size="0.8rem" />} onClick={onRun} loading={!!loading}>
            Run
          </Button>
        </Group>

        <Divider />

        <Group justify="space-between" align="center">
          <SegmentedControl
            size="xs"
            value={viewMode}
            onChange={(val: any) => setViewMode(val)}
            data={[
              { label: 'Result', value: 'transformed' },
              { label: 'Diff', value: 'diff', disabled: !original },
              { label: 'Input', value: 'original', disabled: !original },
            ]}
          />
          <MantineTooltip label={copied ? 'Copied!' : 'Copy to clipboard'}>
            <ActionIcon variant="subtle" color={copied ? 'green' : 'gray'} onClick={copyToClipboard} size="sm">
              {copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />}
            </ActionIcon>
          </MantineTooltip>
        </Group>

        {error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} p="xs">
            <Text size="xs">{error}</Text>
          </Alert>
        ) : (
          <Box flex={1} style={{ position: 'relative', overflow: 'hidden' }}>
            <ScrollArea h="100%" offsetScrollbars type="auto">
              <Code block style={{ fontSize: '11px', background: 'transparent' }}>
                {displayData ? JSON.stringify(displayData, null, 2) : (loading || diffLoading ? '// Loading...' : '// No preview yet')}
              </Code>
            </ScrollArea>
            {viewMode === 'diff' && !loading && !diffLoading && displayData && Object.keys(displayData as any).length === 0 && (
              <Box style={{ position: 'absolute', top: 0, left: 0, right: 0, bottom: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(255,255,255,0.7)' }}>
                <Group gap="xs">
                   <IconGitCompare size="1rem" color="gray" />
                   <Text size="xs" c="dimmed">No changes detected</Text>
                </Group>
              </Box>
            )}
          </Box>
        )}
        
        {isArray && !error && (
          <Text size="10px" c="dimmed" ta="right">
            {(result as any[]).length} items in result
          </Text>
        )}
      </Stack>
    </Card>
  );
}


