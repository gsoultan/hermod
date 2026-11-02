import { useState } from 'react'
import {
  ActionIcon, Alert, Autocomplete, Button, Divider, Group, Input, ScrollArea, SegmentedControl, Select, Stack, Switch, Text,
} from '@mantine/core'
import { IconAlertTriangle, IconCircleCheck, IconInfoCircle, IconPlayerPlay, IconPlus, IconTrash } from '@tabler/icons-react'
import { TextInput } from '@/components/shared/ReferenceInputs'
import { TemplateField } from '@/components/shared/TemplateField'
import { FormRow } from '@/components/common/FormRow'
import type { APILookupTestOutcome } from './APILookupConfig'

/** The engine's defaults (pkg/comm/transformer/lookup/metis_decide.go). */
export const METIS_DECIDE_DEFAULTS = { targetField: 'decision', timeout: '10s', onNoMatch: 'keep' } as const

const NO_MATCH = [
  { value: 'keep', label: 'Keep the record (matched: false)' },
  { value: 'drop', label: 'Drop the record' },
  { value: 'fail', label: 'Fail the record (dead letter)' },
]

interface MetisInput {
  name: string
  field: string
}

interface MetisDecideConfigProps {
  config: Record<string, any>
  nodeId: string
  updateNodeConfig: (nodeId: string, patch: Record<string, unknown>) => void
  fieldPaths?: string[]
  testLookup?: () => void
  testing?: boolean
  lastTest?: APILookupTestOutcome | null
}

const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v))

/**
 * metis_decide: run one of a Metis project's DMN decision tables on the record
 * and write its outputs into it. The server, account and project take secrets
 * only (ResolveTemplateScoped), so a record can never choose where the token
 * goes; the table's key may follow the record.
 */
export function MetisDecideConfig({
  config,
  nodeId,
  updateNodeConfig,
  fieldPaths = [],
  testLookup,
  testing,
  lastTest,
}: MetisDecideConfigProps) {
  const set = (patch: Record<string, unknown>) => updateNodeConfig(nodeId, patch)
  const missing = (v: unknown) => (str(v).trim() ? undefined : 'Required')

  const [auth, setAuth] = useState<'token' | 'login'>(str(config.username) && !str(config.token) ? 'login' : 'token')
  const chooseAuth = (v: string) => {
    setAuth(v as 'token' | 'login')
    // The engine prefers the token when both are set, so only one is kept.
    set(v === 'login' ? { token: '' } : { username: '', password: '' })
  }

  const inputs: MetisInput[] = Array.isArray(config.inputs) ? config.inputs : []
  // New nodes start on chosen fields (the palette seeds one empty row), so the
  // whole record is sent only when someone chooses that.
  const [inputMode, setInputMode] = useState<'all' | 'fields'>(Array.isArray(config.inputs) && inputs.length ? 'fields' : 'all')
  const chooseInputMode = (v: string) => {
    setInputMode(v as 'all' | 'fields')
    if (v === 'all') set({ inputs: [] })
    else if (!inputs.length) set({ inputs: [{ name: '', field: '' }] })
  }
  const setInput = (i: number, patch: Partial<MetisInput>) =>
    set({ inputs: inputs.map((row, j) => (j === i ? { ...row, ...patch } : row)) })

  const version = str(config.version).trim()
  const keyError = /\{\{/.test(str(config.decisionKey)) ? 'A fixed key; record fields are not read here' : undefined
  const versionError = version && !/^[1-9]\d*$/.test(version) ? 'A version number, or empty for the live version' : undefined

  return (
    <Stack gap="md">
      <Alert icon={<IconInfoCircle size="1rem" />} color="teal" variant="light">
        <Text size="sm">
          Runs one of your Metis decision tables (DMN) on each record and adds its outputs, so the rules stay in Metis
          and a Switch after this step can route on the answer. Nothing changes on the Metis server, so retries and dry
          runs are safe.
        </Text>
      </Alert>

      <Divider label="Metis server" labelPosition="center" />
      <TemplateField
        label="Server address"
        placeholder="https://metis.example.com"
        value={str(config.url)}
        onChange={(url) => set({ url })}
        required
        secretsOnly
        mono
        error={missing(config.url)}
        description="https, or http to localhost only, with no user or password in it. Record values are never read here; a secret is."
      />
      <Input.Wrapper label="Sign in with">
        <SegmentedControl
          mt={4}
          fullWidth
          value={auth}
          onChange={chooseAuth}
          data={[
            { label: 'API token', value: 'token' },
            { label: 'Username and password', value: 'login' },
          ]}
        />
      </Input.Wrapper>
      {auth === 'token' ? (
        <TemplateField
          label="Token"
          placeholder={'{{secret.METIS_TOKEN}}'}
          value={str(config.token)}
          onChange={(token) => set({ token })}
          required
          secretsOnly
          masked
          error={missing(config.token)}
          description="Insert a secret rather than pasting the token."
        />
      ) : (
        <FormRow cols={2}>
          <TemplateField
            label="Username"
            value={str(config.username)}
            onChange={(username) => set({ username })}
            required
            secretsOnly
            error={missing(config.username)}
          />
          <TemplateField
            label="Password"
            placeholder={'{{secret.METIS_PASSWORD}}'}
            value={str(config.password)}
            onChange={(password) => set({ password })}
            required
            secretsOnly
            masked
            error={missing(config.password)}
            description="Signs in again by itself when the session expires."
          />
        </FormRow>
      )}
      <FormRow cols={2}>
        <TextInput
          label="Project ID"
          value={str(config.projectId)}
          onChange={(e) => set({ projectId: e.currentTarget.value })}
          required
          error={missing(config.projectId)}
          description="The Metis project the decision table belongs to."
        />
        <TextInput
          label="Organization ID"
          placeholder="Optional"
          value={str(config.organizationId)}
          onChange={(e) => set({ organizationId: e.currentTarget.value })}
          description="Only for an account in more than one organization."
        />
      </FormRow>

      <Divider label="Decision" labelPosition="center" />
      <FormRow cols={2}>
        <TextInput
          label="Decision key"
          placeholder="e.g. approval-tier"
          value={str(config.decisionKey)}
          onChange={(e) => set({ decisionKey: e.currentTarget.value })}
          required
          error={missing(config.decisionKey) ?? keyError}
          description="The table's key in Metis. Fixed here: a record cannot choose which table runs on it."
        />
        <TextInput
          label="Version"
          placeholder="Live version"
          value={str(config.version)}
          onChange={(e) => set({ version: e.currentTarget.value })}
          error={versionError}
          description="Pin a version to compare a proposed policy with the live one."
        />
      </FormRow>
      <Input.Wrapper label="Inputs">
        <SegmentedControl
          mt={4}
          fullWidth
          value={inputMode}
          onChange={chooseInputMode}
          data={[
            { label: 'The whole record (not its _hermod fields)', value: 'all' },
            { label: 'Chosen fields', value: 'fields' },
          ]}
        />
      </Input.Wrapper>
      {inputMode === 'fields' && (
        <Stack gap="xs">
          {inputs.map((row, i) => (
            <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
              <TextInput
                style={{ flex: 1 }}
                label={i === 0 ? 'Table input' : undefined}
                aria-label={`Table input ${i + 1}`}
                placeholder="e.g. score"
                value={str(row.name)}
                onChange={(e) => setInput(i, { name: e.currentTarget.value })}
              />
              <Autocomplete
                style={{ flex: 1 }}
                label={i === 0 ? 'Record field' : undefined}
                aria-label={`Record field ${i + 1}`}
                placeholder="e.g. customer.credit_score"
                data={fieldPaths}
                value={str(row.field)}
                onChange={(field) => setInput(i, { field })}
              />
              <ActionIcon
                variant="subtle"
                color="red"
                aria-label={`Remove input ${i + 1}`}
                onClick={() => set({ inputs: inputs.filter((_, j) => j !== i) })}
                mb={4}
              >
                <IconTrash size="1rem" />
              </ActionIcon>
            </Group>
          ))}
          <Button
            variant="subtle"
            size="xs"
            leftSection={<IconPlus size="0.8rem" />}
            onClick={() => set({ inputs: [...inputs, { name: '', field: '' }] })}
            style={{ alignSelf: 'flex-start' }}
          >
            Add input
          </Button>
        </Stack>
      )}

      <Divider label="Result" labelPosition="center" />
      <FormRow cols={2}>
        <TextInput
          label="Target field"
          placeholder={METIS_DECIDE_DEFAULTS.targetField}
          value={str(config.targetField)}
          onChange={(e) => set({ targetField: e.currentTarget.value })}
          description="Receives {values, matched, matched_rules, matched_rule_ids, decision: {key, name, version}}."
        />
        <Select
          label="When no rule matches"
          data={NO_MATCH}
          value={str(config.onNoMatch) || METIS_DECIDE_DEFAULTS.onNoMatch}
          onChange={(v) => set({ onNoMatch: v || METIS_DECIDE_DEFAULTS.onNoMatch })}
          allowDeselect={false}
        />
      </FormRow>
      <FormRow cols={2}>
        <Switch
          label="Also copy each output to the top of the record"
          checked={config.mergeOutputs === true || config.mergeOutputs === 'true'}
          onChange={(e) => set({ mergeOutputs: e.currentTarget.checked })}
          mt="sm"
        />
        <TextInput
          label="Timeout"
          placeholder={METIS_DECIDE_DEFAULTS.timeout}
          value={str(config.timeout)}
          onChange={(e) => set({ timeout: e.currentTarget.value })}
          description="How long one decision may take, e.g. 5s. At most 2m."
        />
      </FormRow>

      {testLookup && (
        <Button
          variant="light"
          color="teal"
          fullWidth
          leftSection={<IconPlayerPlay size="0.8rem" />}
          onClick={testLookup}
          onMouseDown={(e) => e.preventDefault()}
          loading={testing}
        >
          Test decision on the sample record
        </Button>
      )}
      {lastTest && (
        <Alert
          data-testid="metis-decide-test-result"
          variant="light"
          color={lastTest.ok ? 'green' : 'red'}
          icon={lastTest.ok ? <IconCircleCheck size="1rem" /> : <IconAlertTriangle size="1rem" />}
          title={lastTest.ok ? 'Last test succeeded' : 'Last test failed'}
          p="sm"
        >
          <ScrollArea.Autosize mah={180} type="auto" offsetScrollbars>
            <Text size="xs" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontFamily: 'var(--mantine-font-family-monospace)' }}>
              {lastTest.message}
            </Text>
          </ScrollArea.Autosize>
        </Alert>
      )}
    </Stack>
  )
}
