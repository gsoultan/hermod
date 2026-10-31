import { useId, useState } from 'react'
import {
  Alert, Button, Group, Modal, NumberInput, SegmentedControl, Stack, Stepper, Text, TextInput, Textarea,
} from '@mantine/core'
import { IconAlertCircle, IconChevronDown, IconChevronRight } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { mlModelsKey, saveModel, type MLBackend, type MLModel, type MimirModel } from '@/lib/mlModels'
import { inputProblems, whereProblems, type ConnectFields, type ConnectProblems } from './connectModel'
import { MimirModelPicker } from './MimirModelPicker'

const splitList = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)

/** Fields kept behind Advanced options; a problem in one opens it. */
const ADVANCED: Array<keyof ConnectFields> = ['tokenSecret', 'inputName']

/** A model's name on the server, as a name Hermod accepts (storage.ValidMLModelName). */
export function hermodName(remote: string): string {
  const cleaned = remote.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^[^a-z]+/, '').replace(/-+$/, '')
  return cleaned.slice(0, 64)
}

/**
 * Connects a model that already runs on a model server, or edits one (`model`
 * given). A model is an address: the server it is served from and its name
 * there; the model file never passes through Hermod. Two short steps, Where it
 * runs and What it takes, each field with one line of help; the rarely needed
 * options stay folded under Advanced. A mistake is caught before Save and the
 * message under the field says how to fix it.
 */
export function ModelFormModal({ vhost, model, onClose }: { vhost: string; model?: MLModel; onClose: () => void }) {
  const editing = model !== undefined
  const queryClient = useQueryClient()
  const serverKindId = useId()
  const [step, setStep] = useState(0)
  const [fields, setFields] = useState<ConnectFields>({
    name: model?.name ?? '',
    backend: model?.backend ?? 'mimir',
    url: model?.url ?? '',
    remoteModel: model?.remote_model ?? '',
    remoteVersion: model?.remote_version ?? '',
    tokenSecret: model?.token_secret ?? '',
    inputName: model?.input_name ?? '',
    features: (model?.features ?? []).join(', '),
  })
  const [timeoutMs, setTimeoutMs] = useState<number | string>(model?.timeout_ms || '')
  const [description, setDescription] = useState(model?.description ?? '')
  const [problems, setProblems] = useState<ConnectProblems>({})
  const [advanced, setAdvanced] = useState(false)
  const oip = fields.backend === 'oip'
  const mimir = fields.backend === 'mimir'
  // What the picked mimir model answers with, to say so under the inputs.
  const [picked, setPicked] = useState<MimirModel | undefined>()

  const set = (key: keyof ConnectFields) => (value: string) => {
    setFields((f) => ({ ...f, [key]: value }))
    setProblems((p) => ({ ...p, [key]: undefined }))
  }
  const text = (key: keyof ConnectFields) => ({
    value: fields[key],
    error: problems[key],
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => set(key)(e.currentTarget.value),
  })

  // A mimir model picked from the server fills what the server knows: its
  // id, its columns as the inputs, its description, and a name in Hermod
  // when none is typed yet.
  const pickMimir = (m: MimirModel) => {
    setPicked(m)
    setFields((f) => ({
      ...f,
      remoteModel: m.id,
      name: editing || f.name.trim() ? f.name : hermodName(m.name),
      features: m.columns?.length ? m.columns.map((c) => c.name).join(', ') : f.features,
    }))
    setProblems((p) => ({ ...p, remoteModel: undefined, name: undefined, features: undefined }))
    if (!description.trim() && m.description) setDescription(m.description)
  }

  // Fetch models needs the address and the key secret to be in shape first.
  const fetchBlocked = (() => {
    const where = whereProblems({ ...fields, remoteModel: 'x' }, true)
    return where.url ?? where.tokenSecret
  })()

  const save = useMutation({
    mutationFn: () =>
      saveModel(vhost, editing ? model.name : fields.name.trim(), {
        backend: fields.backend,
        url: fields.url.trim(),
        remote_model: oip || mimir ? fields.remoteModel.trim() : '',
        remote_version: oip ? fields.remoteVersion.trim() : '',
        token_secret: fields.tokenSecret.trim(),
        input_name: oip ? fields.inputName.trim() : '',
        features: splitList(fields.features),
        timeout_ms: typeof timeoutMs === 'number' ? timeoutMs : 0,
        description: description.trim(),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      onClose()
    },
  })

  const next = () => {
    const found = whereProblems(fields, editing)
    setProblems(found)
    if (Object.keys(found).length === 0) setStep(1)
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (step === 0) {
      next()
      return
    }
    const where = whereProblems(fields, editing)
    if (Object.keys(where).length > 0) {
      setProblems(where)
      setStep(0)
      return
    }
    const found = inputProblems(fields)
    setProblems(found)
    if (ADVANCED.some((k) => found[k])) setAdvanced(true)
    if (Object.keys(found).length === 0) save.mutate()
  }

  return (
    <Modal opened onClose={onClose} title={editing ? `Edit ${model.name}` : 'Connect a model'} centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            For a model that already answers on a model server. Hermod sends it rows and reads back its predictions;
            workflows of {vhost} use it in a Predict node.
          </Text>
          <Stepper active={step} size="sm" allowNextStepsSelect={false} onStepClick={setStep}>
            <Stepper.Step label="Where it runs" description="Its server and name" />
            <Stepper.Step label="What it takes" description="Its inputs" />
          </Stepper>

          {step === 0 && (
            <Stack gap="md">
              {!editing && (
                <TextInput label="Name in Hermod" required placeholder="churn" data-autofocus
                  description="What workflows and apps call it by. Starts with a letter; letters, digits, _ and - after that."
                  {...text('name')} />
              )}
              <Stack gap={4}>
                <Text size="sm" fw={500} id={serverKindId}>Kind of server</Text>
                <SegmentedControl
                  aria-labelledby={serverKindId}
                  value={fields.backend}
                  onChange={(v) => set('backend')(v as MLBackend)}
                  data={[
                    { value: 'mimir', label: 'mimir-server' },
                    { value: 'oip', label: 'Open Inference (KServe, Triton, MLServer)' },
                    { value: 'mlflow', label: 'MLflow' },
                  ]}
                />
                <Text size="xs" c="dimmed">
                  Pick mimir-server for a model uploaded to or trained on your mimir server. Most other model servers speak
                  Open Inference; pick MLflow for a model started with mlflow models serve.
                </Text>
              </Stack>
              <TextInput label="Server address" required
                placeholder={mimir ? 'http://mimir:8080' : oip ? 'http://kserve:8080' : 'http://mlflow-model:5000'}
                description="Where Hermod reaches the server, starting with http:// or https://. Leave out any path."
                {...text('url')} />
              {mimir && (
                <>
                  <TextInput label="API key secret" required placeholder="MIMIR_API_KEY"
                    description={`The name of a secret of ${vhost} holding a key made on the mimir console under API keys. The key itself never goes in here.`}
                    {...text('tokenSecret')} />
                  <MimirModelPicker vhost={vhost} url={fields.url} tokenSecret={fields.tokenSecret} value={fields.remoteModel}
                    error={problems.remoteModel} canFetch={fetchBlocked} onChange={set('remoteModel')} onPick={pickMimir} />
                </>
              )}
              {oip && (
                <Group grow align="flex-start">
                  <TextInput label="Model name on the server" required placeholder="churn"
                    description="As the server lists it; often the same as the name above." {...text('remoteModel')} />
                  <TextInput label="Version" placeholder="1"
                    description="Leave empty for the version the server answers with by default." {...text('remoteVersion')} />
                </Group>
              )}
            </Stack>
          )}

          {step === 1 && (
            <Stack gap="md">
              <TextInput label="Inputs" placeholder="age, tenure, plan"
                description={mimir
                  ? 'The columns the model reads, separated by commas: filled in from the server when the model was picked. The Predict node asks for these.'
                  : 'The fields the model needs, separated by commas, in the order it expects them. The Predict node asks for these.'}
                {...text('features')} data-autofocus />
              {mimir && picked && (
                <Text size="sm" c="dimmed">
                  {picked.labels?.length
                    ? `Answers with one of: ${picked.labels.join(', ')}.`
                    : 'Answers with a number.'}
                  {' '}Each answer also carries its confidence and a prediction id.
                </Text>
              )}
              <Textarea label="Description" autosize minRows={2} placeholder="Predicts whether a customer will cancel."
                description="One sentence for the people who use it." value={description}
                onChange={(e) => setDescription(e.currentTarget.value)} />
              <Button variant="subtle" size="compact-sm" w="fit-content" aria-expanded={advanced}
                leftSection={advanced ? <IconChevronDown size="1rem" /> : <IconChevronRight size="1rem" />}
                onClick={() => setAdvanced((a) => !a)}>
                Advanced options
              </Button>
              {advanced && (
                <Stack gap="md">
                  <Group grow align="flex-start">
                    {!mimir && (
                      <TextInput label="Access token secret" placeholder="ML_TOKEN"
                        description={`Only if the server needs a token: the name of a secret of ${vhost} that holds it.`}
                        {...text('tokenSecret')} />
                    )}
                    <NumberInput label="Timeout (milliseconds)" placeholder="30000" min={0} max={600000}
                      description="How long to wait for an answer. Empty waits 30 seconds."
                      value={timeoutMs} onChange={setTimeoutMs} />
                  </Group>
                  {oip && (
                    <TextInput label="Single input tensor" placeholder="input"
                      description="Only for an exported ONNX model that takes one [rows, inputs] tensor: its name. Empty sends one tensor per input."
                      {...text('inputName')} />
                  )}
                </Stack>
              )}
            </Stack>
          )}

          {save.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not saved">
              {(save.error as Error).message}
            </Alert>
          )}
          <Group justify="space-between">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Group gap="xs">
              {step === 1 && <Button variant="default" onClick={() => setStep(0)}>Back</Button>}
              {step === 0
                ? <Button type="submit">Next</Button>
                : <Button type="submit" loading={save.isPending}>Save model</Button>}
            </Group>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
