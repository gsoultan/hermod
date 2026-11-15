import {
  Accordion,
  ActionIcon,
  Alert,
  Autocomplete,
  Badge,
  Button,
  Code,
  Group,
  MultiSelect,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  TagsInput,
  Text,
  Textarea,
  Tooltip,
} from '@mantine/core';
import { IconArrowDown, IconArrowUp, IconPlus, IconTrash, IconWand } from '@tabler/icons-react';
import { useMemo, useState, type FC } from 'react';
import { PasswordInput, TextInput } from '@/components/shared/ReferenceInputs';
import { FormRow } from '@/components/common/FormRow';
import { apiFetch } from '@/api';
import {
  DRIVERS,
  discoveryType,
  formatStatements,
  formatSteps,
  newStatement,
  newStep,
  parseStatements,
  parseSteps,
  referenceHints,
  statementProblems,
  statementWarnings,
  stepProblems,
  type TxAction,
  type TxStatement,
  type TxStep,
} from '@/utils/txsql';

export type TxSqlSinkConfigProps = {
  config: any;
  vhost?: string;
  updateConfig: (key: string, value: any) => void;
  availableFields?: any[];
  incomingPayload?: any;
};

type Mode = 'guided' | 'sql';

/** Sinks saved before Guided mode existed have statements and no mode: they are SQL. */
function modeOf(config: any): Mode {
  if (config?.mode === 'guided' || config?.mode === 'sql') return config.mode;
  return typeof config?.statements === 'string' && config.statements.trim() !== '' ? 'sql' : 'guided';
}

function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length) return list;
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

function fieldPaths(fields: any[] | undefined): string[] {
  return (fields ?? [])
    .map((f) => (typeof f === 'string' ? f : f?.path))
    .filter((p): p is string => typeof p === 'string' && p !== '');
}

async function postJSON(url: string, body: unknown): Promise<any> {
  try {
    const res = await apiFetch(url, { method: 'POST', body: JSON.stringify(body), silent: true });
    return await res.json();
  } catch (e: any) {
    throw new Error(e?.data?.error || e?.message || 'The request failed');
  }
}

/**
 * Transactional SQL: write one message to several tables of one database in
 * a single transaction, where a later statement uses what an earlier one gave
 * back. Guided mode builds the statements from tables and fields; SQL mode
 * takes them as written. Both run through the same statement runner.
 */
export const TxSqlSinkConfig: FC<TxSqlSinkConfigProps> = ({ config, vhost, updateConfig, availableFields, incomingPayload }) => {
  const mode = modeOf(config);
  const driver: string = config?.driver || '';

  const setMode = (m: Mode) => {
    updateConfig('mode', m);
  };

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        Every message runs these steps in order, in one transaction on one database: all of them commit, or none do. A
        later step can use what an earlier one gave back, such as a new order&apos;s id.
      </Text>

      <ConnectionFields config={config} updateConfig={updateConfig} />

      <Stack gap={4}>
        <Text size="sm" fw={500}>
          How to describe the writes
        </Text>
        <SegmentedControl
          value={mode}
          onChange={(v) => setMode(v as Mode)}
          data={[
            { value: 'guided', label: 'Guided' },
            { value: 'sql', label: 'SQL' },
          ]}
        />
      </Stack>

      {mode === 'guided' ? (
        <GuidedSteps
          config={config}
          vhost={vhost}
          driver={driver}
          updateConfig={updateConfig}
          fields={fieldPaths(availableFields)}
        />
      ) : (
        <SqlStatements config={config} driver={driver} updateConfig={updateConfig} />
      )}

      <TestPanel config={config} vhost={vhost} incomingPayload={incomingPayload} />

      <Accordion variant="contained" radius="md" keepMounted={false}>
        <Accordion.Item value="advanced">
          <Accordion.Control>Advanced</Accordion.Control>
          <Accordion.Panel>
            <Stack gap="sm">
              <Select
                label="When a field is missing from the message"
                data={[
                  { value: 'fail', label: 'Refuse the message (recommended)' },
                  { value: 'null', label: 'Write NULL' },
                ]}
                value={config?.on_unresolved || 'fail'}
                onChange={(v) => updateConfig('on_unresolved', v === 'fail' ? '' : v || '')}
                description="Refusing stops a child row being written with no parent id when a field name is mistyped."
                allowDeselect={false}
              />
              <Select
                label="Isolation"
                data={[
                  { value: '', label: "The database's default" },
                  { value: 'read_committed', label: 'Read committed' },
                  { value: 'repeatable_read', label: 'Repeatable read' },
                  { value: 'serializable', label: 'Serializable' },
                ]}
                value={config?.isolation || ''}
                onChange={(v) => updateConfig('isolation', v || '')}
                allowDeselect={false}
              />
              <TextInput
                label="Give up on one statement after"
                placeholder="30s"
                value={config?.statement_timeout || ''}
                onChange={(e) => updateConfig('statement_timeout', e.currentTarget.value)}
                description="A statement waiting this long on a lock fails the batch, which is then retried."
              />
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  );
};

const ConnectionFields: FC<{ config: any; updateConfig: (k: string, v: any) => void }> = ({ config, updateConfig }) => {
  const driver = config?.driver || '';
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => updateConfig(k, e.currentTarget.value);
  return (
    <Stack gap="sm">
      <Select
        label="Database"
        placeholder="Choose the database type"
        data={DRIVERS}
        value={driver || null}
        onChange={(v) => updateConfig('driver', v || '')}
        required
        allowDeselect={false}
      />
      {driver === 'sqlite' ? (
        <TextInput label="Database file" placeholder="/data/app.db" value={config?.path || ''} onChange={set('path')} required />
      ) : driver ? (
        <>
          <FormRow>
            <TextInput label="Host" placeholder="localhost" value={config?.host || ''} onChange={set('host')} />
            <TextInput
              label="Port"
              placeholder={driver === 'mysql' || driver === 'mariadb' ? '3306' : driver === 'mssql' ? '1433' : '5432'}
              value={config?.port || ''}
              onChange={set('port')}
            />
          </FormRow>
          <FormRow>
            <TextInput label="User" value={config?.user || ''} onChange={set('user')} />
            <PasswordInput label="Password" value={config?.password || ''} onChange={set('password')} />
          </FormRow>
          <TextInput label="Database name" value={config?.dbname || ''} onChange={set('dbname')} />
          <TextInput
            label="Or a connection string"
            placeholder="Used instead of the fields above when set"
            value={config?.connection_string || ''}
            onChange={set('connection_string')}
          />
        </>
      ) : null}
    </Stack>
  );
};

// ---------------------------------------------------------------- SQL mode

const SqlStatements: FC<{ config: any; driver: string; updateConfig: (k: string, v: any) => void }> = ({
  config,
  driver,
  updateConfig,
}) => {
  const list = parseStatements(config?.statements);
  const problems = statementProblems(list);
  const save = (next: TxStatement[]) => updateConfig('statements', formatStatements(next));
  const patch = (i: number, p: Partial<TxStatement>) => save(list.map((s, k) => (k === i ? { ...s, ...p } : s)));

  return (
    <Stack gap="sm">
      {list.length === 0 && (
        <Text size="sm" c="dimmed">
          No statements yet. Add the parent&apos;s write first: the statements run top to bottom.
        </Text>
      )}
      {list.map((s, i) => {
        const hints = referenceHints(list, i, driver);
        const warnings = statementWarnings(s);
        return (
          <Paper key={i} withBorder p="sm" radius="md">
            <Stack gap="xs">
              <Group justify="space-between" wrap="nowrap">
                <Group gap="xs">
                  <Badge variant="light">{i + 1}</Badge>
                  <TextInput
                    aria-label="Statement name"
                    value={s.name}
                    onChange={(e) => patch(i, { name: e.currentTarget.value })}
                    size="xs"
                    w={160}
                  />
                </Group>
                <CardActions
                  index={i}
                  count={list.length}
                  onMove={(to) => save(move(list, i, to))}
                  onRemove={() => save(list.filter((_, k) => k !== i))}
                />
              </Group>
              <Textarea
                label="SQL"
                autosize
                minRows={2}
                ff="monospace"
                value={s.sql}
                onChange={(e) => patch(i, { sql: e.currentTarget.value })}
                error={problems[i]}
              />
              {hints.length > 0 && (
                <Group gap={4}>
                  <Text size="xs" c="dimmed">
                    You can use:
                  </Text>
                  {hints.map((h) => (
                    <Code key={h}>{h}</Code>
                  ))}
                </Group>
              )}
              {warnings.map((w) => (
                <Text key={w} size="xs" c="orange">
                  {w}
                </Text>
              ))}
              <FormRow>
                <TextInput
                  label="Run once for each item in"
                  placeholder="items"
                  value={s.forEach || ''}
                  onChange={(e) => patch(i, { forEach: e.currentTarget.value })}
                  description="A list field. Leave empty to run once."
                  size="xs"
                />
                <TextInput
                  label="Call each item"
                  placeholder="item"
                  value={s.as || ''}
                  onChange={(e) => patch(i, { as: e.currentTarget.value })}
                  disabled={!s.forEach}
                  size="xs"
                />
                <Select
                  label="Must"
                  size="xs"
                  data={[
                    { value: '', label: 'Default' },
                    { value: 'one', label: 'Return or change exactly 1 row' },
                    { value: 'affected', label: 'Change at least 1 row' },
                    { value: 'any', label: 'No check' },
                  ]}
                  value={s.expect || ''}
                  onChange={(v) => patch(i, { expect: (v || '') as TxStatement['expect'] })}
                  allowDeselect={false}
                />
              </FormRow>
            </Stack>
          </Paper>
        );
      })}
      <Group>
        <Button variant="light" leftSection={<IconPlus size={16} />} onClick={() => save([...list, newStatement(list.length)])}>
          Add statement
        </Button>
      </Group>
    </Stack>
  );
};

const CardActions: FC<{ index: number; count: number; onMove: (to: number) => void; onRemove: () => void }> = ({
  index,
  count,
  onMove,
  onRemove,
}) => (
  <Group gap={4} wrap="nowrap">
    <ActionIcon variant="subtle" aria-label="Move up" disabled={index === 0} onClick={() => onMove(index - 1)}>
      <IconArrowUp size={16} />
    </ActionIcon>
    <ActionIcon variant="subtle" aria-label="Move down" disabled={index === count - 1} onClick={() => onMove(index + 1)}>
      <IconArrowDown size={16} />
    </ActionIcon>
    <ActionIcon variant="subtle" color="red" aria-label="Remove" onClick={onRemove}>
      <IconTrash size={16} />
    </ActionIcon>
  </Group>
);

// ------------------------------------------------------------- Guided mode

type ColumnInfo = { name: string; type?: string; is_pk?: boolean; is_identity?: boolean };

const ACTIONS: { value: TxAction; label: string }[] = [
  { value: 'upsert', label: 'Insert or update (recommended)' },
  { value: 'insert', label: 'Insert' },
  { value: 'update', label: 'Update' },
  { value: 'delete', label: 'Delete' },
];

const GuidedSteps: FC<{
  config: any;
  vhost?: string;
  driver: string;
  updateConfig: (k: string, v: any) => void;
  fields: string[];
}> = ({ config, vhost, driver, updateConfig, fields }) => {
  const steps = parseSteps(config?.steps);
  const problems = stepProblems(steps);
  const save = (next: TxStep[]) => updateConfig('steps', formatSteps(next));
  const patch = (i: number, p: Partial<TxStep>) => save(steps.map((s, k) => (k === i ? { ...s, ...p } : s)));

  const [tables, setTables] = useState<string[]>([]);
  const [columns, setColumns] = useState<Record<string, ColumnInfo[]>>({});
  const [discoveryError, setDiscoveryError] = useState('');
  const [compiled, setCompiled] = useState<TxStatement[] | null>(null);
  const [compileError, setCompileError] = useState('');
  const [confirmConvert, setConfirmConvert] = useState(false);

  const sink = { type: discoveryType(driver), vhost, config };

  const loadTables = async () => {
    setDiscoveryError('');
    try {
      const got = await postJSON('/api/sinks/discover/tables', sink);
      setTables(Array.isArray(got) ? got : []);
    } catch (e: any) {
      setDiscoveryError(e.message);
    }
  };

  const loadColumns = async (table: string): Promise<ColumnInfo[]> => {
    if (columns[table]) return columns[table];
    try {
      const got = await postJSON('/api/sinks/discover/columns', { sink, table });
      const list = Array.isArray(got) ? (got as ColumnInfo[]) : [];
      setColumns((c) => ({ ...c, [table]: list }));
      return list;
    } catch (e: any) {
      setDiscoveryError(e.message);
      return [];
    }
  };

  // Map every column the table has to the field of the same name, match on
  // its primary key, and give back the generated key.
  const fillFromTable = async (i: number) => {
    const s = steps[i];
    const cols = await loadColumns(s.table);
    if (cols.length === 0) return;
    const pick = (col: string) => {
      const lower = col.toLowerCase();
      const hit = fields.find((f) => f.toLowerCase() === lower || f.toLowerCase().endsWith(`.${lower}`));
      return hit ? `{{${hit}}}` : '';
    };
    const writable = cols.filter((c) => !c.is_identity);
    patch(i, {
      columns: writable.map((c) => ({ column: c.name, value: pick(c.name) })),
      match: cols.filter((c) => c.is_pk && !c.is_identity).map((c) => c.name),
      returning: cols.filter((c) => c.is_identity || c.is_pk).map((c) => c.name).slice(0, 1),
    });
  };

  const compile = async (): Promise<TxStatement[] | null> => {
    setCompileError('');
    try {
      const got = await postJSON('/api/sinks/txsql/compile', { config: { ...config, mode: 'guided' } });
      const list = (got?.statements ?? []) as TxStatement[];
      setCompiled(list);
      return list;
    } catch (e: any) {
      setCompileError(e.message);
      return null;
    }
  };

  const convert = async () => {
    const list = await compile();
    if (!list) return;
    updateConfig('statements', formatStatements(parseStatements(JSON.stringify(list))));
    updateConfig('mode', 'sql');
    setConfirmConvert(false);
  };

  // What each step can take a value from: message fields, an earlier step's
  // given-back columns, and the current item.
  const valueOptions = (i: number) => {
    const s = steps[i];
    const opts = fields.map((f) => `{{${f}}}`);
    steps.slice(0, i).forEach((p, k) => {
      if (p.forEach) return;
      const name = p.name || `step${k + 1}`;
      for (const r of p.returning) opts.push(`{{${name}.${r}}}`);
    });
    if (s.forEach) opts.push(`{{${s.as || 'item'}}}`);
    return [...new Set(opts)];
  };

  return (
    <Stack gap="sm">
      <Group gap="xs">
        <Button variant="default" size="xs" onClick={loadTables} disabled={!driver}>
          Load tables
        </Button>
        {discoveryError && (
          <Text size="xs" c="red">
            {discoveryError}
          </Text>
        )}
      </Group>
      {steps.length === 0 && (
        <Text size="sm" c="dimmed">
          No steps yet. Add the parent table first: steps run top to bottom.
        </Text>
      )}
      {steps.map((s, i) => {
        const cols = (columns[s.table] ?? []).map((c) => c.name);
        const setCols = s.columns.map((c) => c.column).filter(Boolean);
        return (
          <Paper key={i} withBorder p="sm" radius="md">
            <Stack gap="xs">
              <Group justify="space-between" wrap="nowrap">
                <Group gap="xs">
                  <Badge variant="light">{i + 1}</Badge>
                  <TextInput aria-label="Step name" value={s.name} onChange={(e) => patch(i, { name: e.currentTarget.value })} size="xs" w={160} />
                </Group>
                <CardActions
                  index={i}
                  count={steps.length}
                  onMove={(to) => save(move(steps, i, to))}
                  onRemove={() => save(steps.filter((_, k) => k !== i))}
                />
              </Group>
              <FormRow>
                <Autocomplete
                  label="Table"
                  data={tables}
                  value={s.table}
                  onChange={(v) => patch(i, { table: v })}
                  onBlur={() => s.table && loadColumns(s.table)}
                />
                <Select
                  label="Action"
                  data={ACTIONS}
                  value={s.action}
                  onChange={(v) => patch(i, { action: (v || 'upsert') as TxAction })}
                  allowDeselect={false}
                />
              </FormRow>
              <Group justify="space-between">
                <Text size="sm" fw={500}>
                  Columns
                </Text>
                <Tooltip label="Map every column to the field of the same name">
                  <Button size="xs" variant="subtle" leftSection={<IconWand size={14} />} disabled={!s.table} onClick={() => fillFromTable(i)}>
                    Fill from table
                  </Button>
                </Tooltip>
              </Group>
              {s.columns.map((c, k) => (
                <Group key={k} gap="xs" wrap="nowrap" align="flex-end">
                  <Autocomplete
                    aria-label="Column"
                    placeholder="column"
                    data={cols}
                    value={c.column}
                    onChange={(v) => patch(i, { columns: s.columns.map((x, j) => (j === k ? { ...x, column: v } : x)) })}
                    style={{ flex: 1 }}
                  />
                  <Text c="dimmed">←</Text>
                  <Autocomplete
                    aria-label="Value"
                    placeholder="{{field}}"
                    data={valueOptions(i)}
                    value={c.value}
                    onChange={(v) => patch(i, { columns: s.columns.map((x, j) => (j === k ? { ...x, value: v } : x)) })}
                    style={{ flex: 1 }}
                  />
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label="Remove column"
                    onClick={() => patch(i, { columns: s.columns.filter((_, j) => j !== k) })}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
              ))}
              <Group>
                <Button size="xs" variant="subtle" leftSection={<IconPlus size={14} />} onClick={() => patch(i, { columns: [...s.columns, { column: '', value: '' }] })}>
                  Add column
                </Button>
              </Group>
              {s.action !== 'insert' && (
                <MultiSelect
                  label="Match on"
                  description="The key that finds the existing row, such as a primary key or a unique column."
                  data={setCols}
                  value={s.match.filter((m) => setCols.includes(m))}
                  onChange={(v) => patch(i, { match: v })}
                />
              )}
              <FormRow>
                <TextInput
                  label="Repeat for each item in"
                  placeholder="items"
                  value={s.forEach || ''}
                  onChange={(e) => patch(i, { forEach: e.currentTarget.value })}
                  description="A list field, for child rows."
                />
                <TagsInput
                  label="Give back"
                  description="Columns later steps can use, such as the new id."
                  data={cols}
                  value={s.returning}
                  onChange={(v) => patch(i, { returning: v })}
                />
              </FormRow>
              {problems[i] && (
                <Text size="xs" c="red">
                  {problems[i]}
                </Text>
              )}
            </Stack>
          </Paper>
        );
      })}
      <Group justify="space-between">
        <Button variant="light" leftSection={<IconPlus size={16} />} onClick={() => save([...steps, newStep(steps.length)])}>
          Add step
        </Button>
        <Group gap="xs">
          <Button variant="default" size="xs" onClick={compile} disabled={steps.length === 0}>
            Show SQL
          </Button>
          <Button variant="default" size="xs" onClick={() => setConfirmConvert(true)} disabled={steps.length === 0}>
            Convert to SQL mode
          </Button>
        </Group>
      </Group>
      {confirmConvert && (
        <Alert color="yellow" title="Convert to SQL mode?">
          <Stack gap="xs">
            <Text size="sm">
              The steps become SQL you can edit by hand. This is one-way: SQL cannot be turned back into Guided steps.
            </Text>
            <Group gap="xs">
              <Button size="xs" onClick={convert}>
                Convert and edit as SQL
              </Button>
              <Button size="xs" variant="default" onClick={() => setConfirmConvert(false)}>
                Cancel
              </Button>
            </Group>
          </Stack>
        </Alert>
      )}
      {compileError && (
        <Text size="sm" c="red">
          {compileError}
        </Text>
      )}
      {compiled && !compileError && (
        <Paper withBorder p="sm" radius="md">
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              The SQL these steps run
            </Text>
            {compiled.map((st) => (
              <Stack key={st.name} gap={0}>
                <Text size="xs" c="dimmed">
                  {st.name}
                  {st.forEach ? `, for each item in ${st.forEach}` : ''}
                </Text>
                <Code block>{st.sql}</Code>
              </Stack>
            ))}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
};

// ----------------------------------------------------------------- Test

type SimStep = {
  name: string;
  item?: number;
  sql?: string;
  args?: any[];
  rows?: Record<string, any>[];
  row_count: number;
  affected: number;
  duration_ms?: number;
  error?: string;
};

type Simulation = { steps: SimStep[]; error?: string; rolled_back: boolean; notes?: string[] };

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

const TestPanel: FC<{ config: any; vhost?: string; incomingPayload?: any }> = ({ config, vhost, incomingPayload }) => {
  const initialSample = useMemo(() => {
    if (incomingPayload && typeof incomingPayload === 'object') return JSON.stringify(incomingPayload, null, 2);
    return '{\n  "id": "E-1",\n  "customer": "C-9",\n  "total": 120,\n  "items": [{ "sku": "A", "qty": 2 }]\n}';
  }, [incomingPayload]);
  const [sample, setSample] = useState(initialSample);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<Simulation | null>(null);
  const [error, setError] = useState('');

  const run = async () => {
    setError('');
    setResult(null);
    let parsed: any;
    try {
      parsed = sample.trim() ? JSON.parse(sample) : {};
    } catch {
      setError('The sample message is not valid JSON.');
      return;
    }
    setRunning(true);
    try {
      const got = await postJSON('/api/sinks/txsql/test', { vhost, config, sample: parsed });
      setResult(got.simulation as Simulation);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setRunning(false);
    }
  };

  return (
    <Paper withBorder p="sm" radius="md">
      <Stack gap="xs">
        <Group justify="space-between">
          <Text size="sm" fw={600}>
            Try it on a sample message
          </Text>
          <Button size="xs" onClick={run} loading={running}>
            Test
          </Button>
        </Group>
        <Text size="xs" c="dimmed">
          Runs every step against the database and rolls it all back, so nothing is kept.
        </Text>
        <Textarea label="Sample message" autosize minRows={3} maxRows={12} ff="monospace" value={sample} onChange={(e) => setSample(e.currentTarget.value)} />
        {error && (
          <Alert color="red" title="The test could not run">
            {error}
          </Alert>
        )}
        {result && <SimulationReport sim={result} />}
      </Stack>
    </Paper>
  );
};

const SimulationReport: FC<{ sim: Simulation }> = ({ sim }) => (
  <Stack gap="xs">
    {sim.error ? (
      <Alert color="red" title="This message would fail, and nothing would be written">
        {sim.error}
      </Alert>
    ) : (
      <Alert color="green" title={`Every step ran. All of it was rolled back.`}>
        {(sim.notes ?? []).join(' ')}
      </Alert>
    )}
    {sim.steps.map((st, i) => (
      <Paper key={i} withBorder p="xs" radius="sm">
        <Stack gap={2}>
          <Group gap="xs">
            <Text size="sm" fw={500}>
              {st.name}
              {st.item ? ` #${st.item}` : ''}
            </Text>
            <Text size="xs" c={st.error ? 'red' : 'dimmed'}>
              {st.error
                ? st.error
                : st.affected >= 0
                  ? `changed ${plural(st.affected, 'row')}`
                  : `returned ${plural(st.row_count, 'row')}`}
            </Text>
          </Group>
          {st.sql && <Code block>{st.sql}</Code>}
          {st.args && st.args.length > 0 && (
            <Text size="xs" c="dimmed">
              Values: {st.args.map((a) => JSON.stringify(a)).join(', ')}
            </Text>
          )}
          {st.rows && st.rows.length > 0 && (
            <Text size="xs" ff="monospace">
              {st.rows.map((r) => JSON.stringify(r)).join('\n')}
            </Text>
          )}
        </Stack>
      </Paper>
    ))}
  </Stack>
);
