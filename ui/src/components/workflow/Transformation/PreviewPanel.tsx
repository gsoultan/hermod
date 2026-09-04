import { Alert, Badge, Button, Card, Code, Divider, Group, ScrollArea, Stack, Text, SegmentedControl, ActionIcon, Tooltip as MantineTooltip, Box } from '@mantine/core';
import { useEffect, useMemo, useRef, useState } from 'react';
import { IconAlertCircle, IconCheck, IconCopy, IconEye, IconGitCompare, IconPlayerPlay } from '@tabler/icons-react';

interface PreviewPanelProps {
  title?: string;
  loading?: boolean;
  error?: string | null;
  result?: unknown;
  original?: unknown;
  onRun?: () => void;
}

type ViewMode = 'transformed' | 'original' | 'diff';

/** Shallow-ish diff used when the worker is unavailable. Mirrors diffWorker. */
function simpleDiff(orig: any, trans: any): any {
  if (orig === trans) return {};
  if (!orig || typeof orig !== 'object' || !trans || typeof trans !== 'object') return trans;
  const d: any = {};
  for (const k of Object.keys(trans)) {
    if (JSON.stringify(orig[k]) !== JSON.stringify(trans[k])) d[k] = trans[k];
  }
  return d;
}

export function PreviewPanel({ title = 'Preview', loading, error, result, original, onRun }: PreviewPanelProps) {
  const [viewMode, setViewMode] = useState<ViewMode>('transformed');
  const [copied, setCopied] = useState(false);

  const isArray = Array.isArray(result);

  const workerRef = useRef<Worker | null>(null);
  const seqRef = useRef(0);
  const [diffData, setDiffData] = useState<any | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);

  useEffect(() => {
    return () => {
      workerRef.current?.terminate();
      workerRef.current = null;
    };
  }, []);

  // Only diff when the diff tab is actually showing.
  //
  // This effect used to run on every change of `original`/`result` regardless of
  // tab, so the worker was spun up and re-posted even for users who never opened
  // Diff — and because it flipped `diffLoading` on each pass, the pane swapped to
  // a "computing diff…" placeholder and back once per preview.
  const wantsDiff = viewMode === 'diff' && !!original;

  useEffect(() => {
    if (!wantsDiff) return;

    const id = ++seqRef.current;
    const settle = (value: any) => {
      // Ignore anything but the newest request: an older, slower diff must never
      // overwrite a newer one.
      if (id !== seqRef.current) return;
      setDiffData(value);
      setDiffLoading(false);
    };
    const fallback = () => settle(simpleDiff(original as any, result as any));

    try {
      if (!workerRef.current) {
        workerRef.current = new Worker(new URL('../../../workers/diffWorker.ts', import.meta.url), { type: 'module' });
        workerRef.current.onerror = fallback;
        workerRef.current.onmessageerror = fallback;
      }
      setDiffLoading(true);
      workerRef.current.onmessage = (ev: MessageEvent<{ id: number; result: any }>) => {
        if (ev.data?.id === seqRef.current) settle(ev.data.result);
      };
      workerRef.current.postMessage({ id, original, transformed: result });
    } catch {
      fallback();
    }
  }, [wantsDiff, original, result]);

  const displayData = viewMode === 'original' ? original : viewMode === 'diff' ? diffData : result;

  // Serialise once per change rather than on every render.
  //
  // Nothing here clears the displayed value while a refresh is in flight:
  // `previewResult` upstream is only ever assigned on success, and `diffData`
  // below is only replaced by a completed diff. That is what keeps the pane
  // stable — it holds the last result and dims it, rather than dropping to
  // "// Loading..." and back on every re-run.
  const serialised = useMemo(() => {
    if (displayData === undefined || displayData === null) return null;
    try {
      return JSON.stringify(displayData, null, 2);
    } catch {
      return '// Result contains a circular reference and cannot be displayed.';
    }
  }, [displayData]);

  const busy = !!loading || diffLoading;
  const body = serialised ?? (busy ? '// Loading…' : '// No preview yet');
  const showsStaleWhileBusy = busy && serialised !== null;
  const diffIsEmpty =
    viewMode === 'diff' && !busy && !!diffData && typeof diffData === 'object' && Object.keys(diffData).length === 0;

  const copyToClipboard = () => {
    navigator.clipboard.writeText(body);
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
            {/* Trailing item of a space-between row, so showing it shifts
                nothing; the pane's stability comes from holding the last body
                text below, not from reserving this badge's box. */}
            {busy && <Badge color="blue" variant="light" size="xs">Running</Badge>}
          </Group>
          <Button size="compact-xs" variant="light" leftSection={<IconPlayerPlay size="0.8rem" />} onClick={onRun} loading={!!loading}>
            Run Preview
          </Button>
        </Group>

        <Divider />

        <Group justify="space-between" align="center">
          <SegmentedControl
            size="xs"
            value={viewMode}
            onChange={(val) => setViewMode(val as ViewMode)}
            data={[
              { label: 'Result', value: 'transformed' },
              { label: 'Diff', value: 'diff', disabled: !original },
              { label: 'Input', value: 'original', disabled: !original },
            ]}
          />
          <MantineTooltip label={copied ? 'Copied!' : 'Copy to clipboard'}>
            <ActionIcon
              aria-label={copied ? 'Copied to clipboard' : 'Copy preview to clipboard'}
              variant="subtle"
              color={copied ? 'green' : 'gray'}
              onClick={copyToClipboard}
              size="sm"
            >
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
              <Code
                block
                style={{
                  fontSize: '11px',
                  background: 'transparent',
                  // Dim rather than blank: the reader keeps their place and the
                  // pane keeps its height while the next result arrives.
                  opacity: showsStaleWhileBusy ? 0.55 : 1,
                  transition: 'opacity 120ms linear',
                }}
                aria-busy={busy || undefined}
              >
                {body}
              </Code>
            </ScrollArea>
            {diffIsEmpty && (
              <Box style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--mantine-color-body)', opacity: 0.85 }}>
                <Group gap="xs">
                  <IconGitCompare size="1rem" color="gray" />
                  <Text size="xs" c="dimmed">No changes detected</Text>
                </Group>
              </Box>
            )}
          </Box>
        )}

        {isArray && !error && (
          <Text size="xs" c="dimmed" ta="right">
            {(result as any[]).length} items in result
          </Text>
        )}
      </Stack>
    </Card>
  );
}
