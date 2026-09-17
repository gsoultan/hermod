import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  Stepper, Stack, Group, Button, Text, Title, JsonInput, Alert, Card, Badge, Table,
  TextInput, Select, Divider, ScrollArea, Code, List, Loader,
} from '@mantine/core'
import {
  IconFileImport, IconSettings, IconDatabase, IconArrowBigRightLines, IconTransform,
  IconChecklist, IconUpload, IconAlertTriangle, IconInfoCircle, IconCheck,
} from '@tabler/icons-react'
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import {
  parseImportBundle, collectReviewNodes, detectConflicts, applyConflictDecisions,
  buildImportPlan, nameCollisions, takenNames, suggestFreeName,
  type ImportBundle, type ImportResource, type ConflictDecision, type Conflict,
} from '@/utils/importBundle'
import { configComponents } from '@/components/forms/SinkForm'
import { ImportResourceCard, type TestState } from './ImportResourceCard'
import { ImportSourceConfig } from './ImportSourceConfig'
import { ImportNodeCard } from './ImportNodeCard'

const API_BASE = '/api'

interface ImportWizardProps {
  /** The vhost the operator is looking at, used for resources that name none. */
  defaultVHost: string
  availableVHosts: string[]
  onCancel: () => void
  onImported: (workflowName: string) => void
}

type StepKey = 'bundle' | 'workflow' | 'sources' | 'sinks' | 'nodes' | 'review'

/**
 * Importing a workflow, one reviewable step at a time.
 *
 * The old import was a textarea and a button: whatever the file said was written
 * verbatim. That is fine for a bundle produced by the same instance and wrong
 * for every other case — a bundle from staging arrives with staging's
 * hostnames, staging's passwords, staging's encryption key and ids that may
 * already name something in production. None of it was visible until the
 * workflow failed to start, and a colliding id overwrote a live connection
 * without a word.
 *
 * So: parse first, show what is in the file, let the operator fix the values
 * that are environment-specific using the same editors the canvas uses, and
 * only then send one request. The rewriting of ids and references lives in
 * utils/importBundle.ts, which is pure and tested — this file is the shell.
 */
export function ImportWizard({ defaultVHost, availableVHosts, onCancel, onImported }: ImportWizardProps) {
  const [raw, setRaw] = useState('')
  const [bundle, setBundle] = useState<ImportBundle | null>(null)
  const [parseError, setParseError] = useState<string | null>(null)
  const [decisions, setDecisions] = useState<Record<string, ConflictDecision>>({})
  const [active, setActive] = useState(0)
  const [tests, setTests] = useState<Record<string, TestState | null>>({})
  const [testingID, setTestingID] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  // Names this screen chose on the operator's behalf when "copy" was picked,
  // so picking "replace" again can undo them. Without this, changing your mind
  // renames the record you decided to keep.
  const [autoRenamed, setAutoRenamed] = useState<Record<string, { from: string; to: string }>>({})

  // What this instance already holds, so a collision can be named rather than
  // discovered after the fact. A large page size on purpose: a conflict this
  // misses is a silent overwrite, which is the thing being fixed.
  const list = (path: string) => async () => {
    const res = await apiFetch(`${API_BASE}/${path}?limit=1000`, { silent: true })
    const body = await res.json()
    return (Array.isArray(body) ? body : body?.data) ?? []
  }
  const existingSources = useQuery({ queryKey: ['import-existing', 'sources'], queryFn: list('sources') })
  const existingSinks = useQuery({ queryKey: ['import-existing', 'sinks'], queryFn: list('sinks') })
  const existingWorkflows = useQuery({ queryKey: ['import-existing', 'workflows'], queryFn: list('workflows') })

  const existing = useMemo(() => ({
    workflows: existingWorkflows.data ?? [],
    sources: existingSources.data ?? [],
    sinks: existingSinks.data ?? [],
  }), [existingWorkflows.data, existingSources.data, existingSinks.data])

  const loadingExisting =
    existingSources.isLoading || existingSinks.isLoading || existingWorkflows.isLoading

  const conflicts = useMemo(
    () => (bundle ? detectConflicts(bundle, existing) : []),
    [bundle, existing],
  )
  const conflictByID = useMemo(
    () => Object.fromEntries(conflicts.map((c) => [c.id, c])) as Record<string, Conflict>,
    [conflicts],
  )

  const reviewNodes = useMemo(
    () => (bundle ? collectReviewNodes(bundle.workflow) : []),
    [bundle],
  )

  // A name already held by a different record is fatal whichever way the id
  // conflict was resolved: sources.name and sinks.name are UNIQUE, so the
  // insert comes back as a driver-level constraint error. Blocked here, next
  // to the field, rather than reported as "constraint failed (2067)".
  const nameClashes = useMemo(
    () => (bundle ? nameCollisions(bundle, existing) : []),
    [bundle, existing],
  )
  const nameClashByID = useMemo(
    () => Object.fromEntries(nameClashes.map((c) => [c.id, c])),
    [nameClashes],
  )

  // Every source a picker inside a node editor may offer: the bundle's own,
  // then this instance's. A db_lookup can therefore be repointed at a source
  // that is already here instead of importing a second copy of it.
  const pickableSources = useMemo(() => {
    if (!bundle) return existing.sources
    const ids = new Set(bundle.sources.map((s) => s.id))
    return [...bundle.sources, ...existing.sources.filter((s: any) => !ids.has(s.id))]
  }, [bundle, existing.sources])

  const loadBundle = useCallback((text: string) => {
    setRaw(text)
    setSubmitError(null)
    if (!text.trim()) {
      setBundle(null)
      setParseError(null)
      return
    }
    const result = parseImportBundle(text)
    if ('error' in result) {
      setBundle(null)
      setParseError(result.error)
      return
    }
    const parsed = result.bundle
    // A resource that names no vhost belongs where the operator is looking,
    // not in whatever "default" the server would pick for it.
    if (!parsed.workflow.vhost) parsed.workflow.vhost = defaultVHost
    for (const r of [...parsed.sources, ...parsed.sinks]) {
      if (!r.vhost) r.vhost = defaultVHost
    }
    setBundle(parsed)
    setParseError(null)
    setTests({})
  }, [defaultVHost])

  // Default every collision to replacing what is here: that is what importing
  // an updated bundle over its own workflow is for, and it is what this screen
  // did before. The difference is that it is now stated, next to the name of
  // the thing being replaced, with the other choice one click away.
  useEffect(() => {
    if (conflicts.length === 0) return
    setDecisions((current) => {
      const next = { ...current }
      let changed = false
      for (const c of conflicts) {
        if (!next[c.id]) {
          next[c.id] = 'overwrite'
          changed = true
        }
      }
      return changed ? next : current
    })
  }, [conflicts])

  const chooseDecision = (kind: 'sources' | 'sinks', resource: ImportResource, decision: ConflictDecision) => {
    setDecisions((cur) => ({ ...cur, [resource.id]: decision }))

    if (decision === 'copy') {
      // The record being sat beside keeps its name, and the column is UNIQUE,
      // so a copy cannot have the same one. Suggest the nearest free name
      // rather than letting the import fail on a constraint the operator
      // cannot see.
      const taken = takenNames(existing, kind === 'sources' ? 'source' : 'sink')
      const free = suggestFreeName(resource.name, taken)
      if (free !== resource.name) {
        setAutoRenamed((cur) => ({ ...cur, [resource.id]: { from: resource.name, to: free } }))
        patchResource(kind, resource.id, { name: free })
      }
      return
    }

    // Back to replacing: undo the rename, but only if the operator has not
    // since typed a name of their own.
    const auto = autoRenamed[resource.id]
    if (auto && resource.name === auto.to) {
      patchResource(kind, resource.id, { name: auto.from })
      setAutoRenamed(({ [resource.id]: _dropped, ...rest }) => rest)
    }
  }

  const patchResource = (kind: 'sources' | 'sinks', id: string, patch: Partial<ImportResource>) => {
    setBundle((b) => b && ({
      ...b,
      [kind]: b[kind].map((r) => (r.id === id ? { ...r, ...patch } : r)),
    }))
    setTests((t) => ({ ...t, [id]: null }))
  }

  const patchResourceConfig = (kind: 'sources' | 'sinks', id: string, key: string, value: any) => {
    setBundle((b) => b && ({
      ...b,
      [kind]: b[kind].map((r) => (r.id === id ? { ...r, config: { ...r.config, [key]: value } } : r)),
    }))
    setTests((t) => ({ ...t, [id]: null }))
  }

  const patchNodeConfig = (nodeID: string, patch: any, replace?: boolean) => {
    setBundle((b) => b && ({
      ...b,
      workflow: {
        ...b.workflow,
        nodes: b.workflow.nodes.map((n) =>
          n.id === nodeID ? { ...n, config: replace ? patch : { ...(n.config ?? {}), ...patch } } : n,
        ),
      },
    }))
  }

  const runTest = async (kind: 'sources' | 'sinks', resource: ImportResource) => {
    setTestingID(resource.id)
    try {
      const config = Object.fromEntries(
        Object.entries(resource.config ?? {}).filter(([, v]) => v !== ''),
      )
      const res = await apiFetch(`${API_BASE}/${kind}/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...resource, config }),
        silent: true,
      })
      const data = await res.json().catch(() => ({}))
      setTests((t) => ({
        ...t,
        [resource.id]: res.ok
          ? { status: 'ok', message: 'Connected.' }
          : { status: 'error', message: data.error || `Connection failed (HTTP ${res.status}).` },
      }))
    } catch (err: any) {
      setTests((t) => ({ ...t, [resource.id]: { status: 'error', message: err?.message || 'Connection failed.' } }))
    } finally {
      setTestingID(null)
    }
  }

  const finalBundle = useMemo(
    () => (bundle ? applyConflictDecisions(bundle, decisions) : null),
    [bundle, decisions],
  )
  const plan = useMemo(
    () => (finalBundle ? buildImportPlan(finalBundle, existing) : []),
    [finalBundle, existing],
  )

  const submit = async () => {
    if (!finalBundle) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      // missing_refs is the export's report about the instance that produced
      // the file. Sending it back would be noise; the server ignores unknown
      // fields, but leaving it out keeps the request honest.
      const { missing_refs: _ignored, ...payload } = finalBundle
      const res = await apiFetch(`${API_BASE}/workflows/import`, {
        method: 'POST',
        body: JSON.stringify(payload),
        silent: true,
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `Import failed (HTTP ${res.status}).`)
      }
      onImported(finalBundle.workflow.name)
    } catch (err: any) {
      setSubmitError(err?.message || 'Import failed.')
    } finally {
      setSubmitting(false)
    }
  }

  const steps: { key: StepKey; label: string; description: string; icon: ReactNode }[] = useMemo(() => {
    const s: { key: StepKey; label: string; description: string; icon: ReactNode }[] = [
      { key: 'bundle', label: 'Bundle', description: 'Paste or upload', icon: <IconFileImport size="1.1rem" /> },
    ]
    if (!bundle) return s
    s.push({ key: 'workflow', label: 'Workflow', description: 'Name & vhost', icon: <IconSettings size="1.1rem" /> })
    if (bundle.sources.length) {
      s.push({ key: 'sources', label: 'Sources', description: `${bundle.sources.length} to review`, icon: <IconDatabase size="1.1rem" /> })
    }
    if (bundle.sinks.length) {
      s.push({ key: 'sinks', label: 'Sinks', description: `${bundle.sinks.length} to review`, icon: <IconArrowBigRightLines size="1.1rem" /> })
    }
    if (reviewNodes.length) {
      s.push({ key: 'nodes', label: 'Nodes', description: reviewNodes.length === 1 ? '1 needs a look' : `${reviewNodes.length} need a look`, icon: <IconTransform size="1.1rem" /> })
    }
    s.push({ key: 'review', label: 'Review', description: 'Confirm & import', icon: <IconChecklist size="1.1rem" /> })
    return s
  }, [bundle, reviewNodes.length])

  // A step count that shrinks (a bundle with no sinks, say) must not strand the
  // stepper past its own end.
  useEffect(() => {
    setActive((a) => Math.min(a, Math.max(0, steps.length - 1)))
  }, [steps.length])

  const currentKey = steps[active]?.key ?? 'bundle'
  const canAdvance = currentKey !== 'bundle' || (!!bundle && !loadingExisting)

  const onFile = (file: File | null) => {
    if (!file) return
    const reader = new FileReader()
    reader.onload = (e) => loadBundle(String(e.target?.result ?? ''))
    reader.readAsText(file)
  }

  return (
    <Stack gap="md">
      <Stepper active={active} onStepClick={setActive} allowNextStepsSelect={false} size="sm">
        {steps.map((s) => (
          <Stepper.Step
            key={s.key}
            label={s.label}
            description={s.description}
            icon={s.icon}
            completedIcon={<IconCheck size="1.1rem" />}
          />
        ))}
      </Stepper>

      <ScrollArea.Autosize mah="58vh" type="auto" offsetScrollbars>
        <Stack gap="md" pr="xs">
          {currentKey === 'bundle' && (
            <Stack gap="md">
              <Group justify="space-between" align="flex-end">
                <div>
                  <Title order={5}>The bundle</Title>
                  <Text size="sm" c="dimmed">Paste the JSON an export produced, or upload the file.</Text>
                </div>
                <Button variant="light" component="label" leftSection={<IconUpload size="1rem" />}>
                  Upload file
                  <input
                    type="file"
                    hidden
                    accept=".json,application/json"
                    onChange={(e) => onFile(e.currentTarget.files?.[0] ?? null)}
                  />
                </Button>
              </Group>

              <JsonInput
                placeholder='{ "workflow": { ... }, "sources": [ ... ], "sinks": [ ... ] }'
                autosize
                minRows={12}
                maxRows={20}
                value={raw}
                onChange={loadBundle}
                error={parseError ?? undefined}
              />

              {bundle && (
                <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />} title={bundle.workflow.name}>
                  <Group gap="xs" mt={4}>
                    <Badge variant="light">{bundle.workflow.nodes.length} nodes</Badge>
                    <Badge variant="light">{bundle.sources.length} sources</Badge>
                    <Badge variant="light">{bundle.sinks.length} sinks</Badge>
                    {reviewNodes.length > 0 && (
                      <Badge color="orange" variant="light">
                        {reviewNodes.length === 1 ? '1 node to review' : `${reviewNodes.length} nodes to review`}
                      </Badge>
                    )}
                    {conflicts.length > 0 && (
                      <Badge color="orange" variant="light">
                        {conflicts.length === 1 ? '1 id collision' : `${conflicts.length} id collisions`}
                      </Badge>
                    )}
                  </Group>
                </Alert>
              )}

              {bundle?.missing_refs?.length ? (
                <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />} title="The export could not include everything">
                  <Text size="sm">
                    The instance that produced this file could not find these, so they are not in it:
                  </Text>
                  <List size="sm" mt={6}>
                    {bundle.missing_refs.map((m) => (
                      <List.Item key={`${m.kind}-${m.id}`}>
                        <Code>{m.kind} {m.id}</Code>{m.node_id ? ` (node ${m.node_id})` : ''}
                      </List.Item>
                    ))}
                  </List>
                  <Text size="sm" mt={6}>
                    The workflow will import, but it cannot run until those references point at
                    something that exists here. Where the reference is on a node, the Nodes step
                    lets you repoint it now.
                  </Text>
                </Alert>
              ) : null}

              {loadingExisting && (
                <Group gap="xs">
                  <Loader size="xs" />
                  <Text size="sm" c="dimmed">Checking what is already on this instance…</Text>
                </Group>
              )}
            </Stack>
          )}

          {currentKey === 'workflow' && bundle && (
            <Card withBorder radius="md" padding="lg">
              <Stack gap="md">
                <div>
                  <Title order={5}>The workflow</Title>
                  <Text size="sm" c="dimmed">How it is named and where it lands on this instance.</Text>
                </div>
                <Divider />
                {conflictByID[bundle.workflow.id] && (
                  <Alert color="orange" variant="light" icon={<IconAlertTriangle size="1rem" />}>
                    <Stack gap="xs">
                      <Text size="sm">
                        A workflow with id <Code>{bundle.workflow.id}</Code> already exists here, named{' '}
                        <strong>{conflictByID[bundle.workflow.id].existingName}</strong>.
                      </Text>
                      <Select
                        label="What should happen to it"
                        data={[
                          { value: 'overwrite', label: 'Replace it with this one' },
                          { value: 'copy', label: 'Import alongside it, as a new workflow' },
                        ]}
                        value={decisions[bundle.workflow.id] ?? 'overwrite'}
                        onChange={(v) => setDecisions((d) => ({ ...d, [bundle.workflow.id]: (v as ConflictDecision) || 'overwrite' }))}
                        allowDeselect={false}
                      />
                    </Stack>
                  </Alert>
                )}
                <Group grow align="flex-start">
                  <TextInput
                    label="Name"
                    value={bundle.workflow.name ?? ''}
                    onChange={(e) => {
                      // Read the value here, not inside the updater: React
                      // clears currentTarget once the handler returns, and a
                      // functional update can run after that — which threw,
                      // took the whole wizard down and closed the modal.
                      const name = e.currentTarget.value
                      setBundle((b) => b && ({ ...b, workflow: { ...b.workflow, name } }))
                    }}
                    required
                  />
                  <Select
                    label="Virtual host"
                    data={availableVHosts}
                    value={bundle.workflow.vhost || ''}
                    onChange={(v) => setBundle((b) => b && ({ ...b, workflow: { ...b.workflow, vhost: v || '' } }))}
                    searchable
                  />
                </Group>
                <Alert color="gray" variant="light" icon={<IconInfoCircle size="1rem" />}>
                  <Text size="sm">
                    The workflow imports stopped. Start it from the list once you have checked it
                    on the canvas.
                  </Text>
                </Alert>
              </Stack>
            </Card>
          )}

          {currentKey === 'sources' && bundle && (
            <Stack gap="md">
              <div>
                <Title order={5}>Sources</Title>
                <Text size="sm" c="dimmed">
                  Hostnames and credentials travel with the bundle and almost never survive the
                  trip. Point each one at this environment.
                </Text>
              </div>
              {bundle.sources.map((src) => (
                <ImportResourceCard
                  key={src.id}
                  kind="source"
                  resource={src}
                  conflict={conflictByID[src.id]}
                  decision={decisions[src.id] ?? 'overwrite'}
                  onDecision={(d) => chooseDecision('sources', src, d)}
                  nameError={nameClashByID[src.id]
                    ? 'Another source here already has this name, and the column is unique. Choose a different one.'
                    : undefined}
                  onChange={(patch) => patchResource('sources', src.id, patch)}
                  vhostOptions={availableVHosts}
                  onTest={() => runTest('sources', src)}
                  testing={testingID === src.id}
                  testState={tests[src.id]}
                >
                  <ImportSourceConfig
                    source={src}
                    updateConfig={(key, value) => patchResourceConfig('sources', src.id, key, value)}
                    allSources={pickableSources}
                  />
                </ImportResourceCard>
              ))}
            </Stack>
          )}

          {currentKey === 'sinks' && bundle && (
            <Stack gap="md">
              <div>
                <Title order={5}>Sinks</Title>
                <Text size="sm" c="dimmed">Where this workflow writes, on this instance.</Text>
              </div>
              {bundle.sinks.map((snk) => {
                // configComponents has no entry for a type this build does not
                // offer. Falling through to the database form is how a webhook
                // sink came to ask for a host and a table, so an unknown type
                // gets said out loud instead.
                const Config = configComponents[snk.type]
                return (
                  <ImportResourceCard
                    key={snk.id}
                    kind="sink"
                    resource={snk}
                    conflict={conflictByID[snk.id]}
                    decision={decisions[snk.id] ?? 'overwrite'}
                    onDecision={(d) => chooseDecision('sinks', snk, d)}
                    nameError={nameClashByID[snk.id]
                      ? 'Another sink here already has this name, and the column is unique. Choose a different one.'
                      : undefined}
                    onChange={(patch) => patchResource('sinks', snk.id, patch)}
                    vhostOptions={availableVHosts}
                    onTest={() => runTest('sinks', snk)}
                    testing={testingID === snk.id}
                    testState={tests[snk.id]}
                  >
                    {Config ? (
                      <Config
                        type={snk.type}
                        config={snk.config}
                        updateConfig={(key: string, value: any) => patchResourceConfig('sinks', snk.id, key, value)}
                        handleSinkChange={(field: string, value: any) => patchResource('sinks', snk.id, { [field]: value })}
                      />
                    ) : (
                      <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />}>
                        This build has no form for a <Code>{snk.type}</Code> sink, so it imports
                        exactly as the file describes it. Open it from the Sinks page afterwards to
                        change anything.
                      </Alert>
                    )}
                  </ImportResourceCard>
                )
              })}
            </Stack>
          )}

          {currentKey === 'nodes' && bundle && (
            <Stack gap="md">
              <div>
                <Title order={5}>Nodes that hold something environment-specific</Title>
                <Text size="sm" c="dimmed">
                  A lookup points at a source, an API call names an address, an encrypt node carries
                  a key. The rest of the workflow describes a shape and travels unchanged, so it is
                  not listed here.
                </Text>
              </div>
              {reviewNodes.map((review) => (
                <ImportNodeCard
                  key={review.node.id}
                  review={{
                    ...review,
                    // The card renders the live node, not the parsed snapshot,
                    // so edits made here are the ones shown back.
                    node: bundle.workflow.nodes.find((n) => n.id === review.node.id) ?? review.node,
                  }}
                  sources={pickableSources}
                  onChange={patchNodeConfig}
                />
              ))}
            </Stack>
          )}

          {currentKey === 'review' && finalBundle && (
            <Stack gap="md">
              <div>
                <Title order={5}>What will happen</Title>
                <Text size="sm" c="dimmed">One request writes all of this.</Text>
              </div>

              {plan.some((r) => r.action === 'update') && (
                <Alert color="orange" variant="light" icon={<IconAlertTriangle size="1rem" />}>
                  Rows marked <strong>replace</strong> overwrite a record that exists here. Anything
                  already using it starts using the settings below.
                </Alert>
              )}

              <Table withTableBorder highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>What</Table.Th>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Action</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {plan.map((row) => (
                    <Table.Tr key={`${row.kind}-${row.id}`}>
                      <Table.Td><Badge variant="light" size="sm">{row.kind}</Badge></Table.Td>
                      <Table.Td>{row.name}</Table.Td>
                      <Table.Td>
                        <Badge color={row.action === 'update' ? 'orange' : 'green'} variant="light" size="sm">
                          {row.action === 'update' ? 'replace' : 'create'}
                        </Badge>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>

              {Object.entries(tests).some(([, t]) => t?.status === 'error') && (
                <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />}>
                  At least one connection test failed. You can still import — the workflow arrives
                  stopped — but it will not run until the connection works.
                </Alert>
              )}

              {nameClashes.length > 0 && (
                <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Two names are taken">
                  <Text size="sm">
                    These names already belong to a different record here, and the column is unique —
                    the import would fail on a constraint. Rename them on the step above.
                  </Text>
                  <List size="sm" mt={6}>
                    {nameClashes.map((c) => (
                      <List.Item key={`${c.kind}-${c.id}`}><Code>{c.kind}</Code> {c.name}</List.Item>
                    ))}
                  </List>
                </Alert>
              )}

              {submitError && (
                <Alert color="red" variant="light" title="Import failed">{submitError}</Alert>
              )}
            </Stack>
          )}
        </Stack>
      </ScrollArea.Autosize>

      <Divider />

      <Group justify="space-between">
        <Button variant="subtle" color="gray" onClick={onCancel}>Cancel</Button>
        <Group>
          <Button variant="default" onClick={() => setActive((a) => Math.max(0, a - 1))} disabled={active === 0}>
            Back
          </Button>
          {currentKey === 'review' ? (
            <Button onClick={submit} loading={submitting} disabled={nameClashes.length > 0}>
              Import workflow
            </Button>
          ) : (
            <Button
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => setActive((a) => Math.min(steps.length - 1, a + 1))}
              disabled={!canAdvance}
            >
              Next
            </Button>
          )}
        </Group>
      </Group>
    </Stack>
  )
}

export default ImportWizard
