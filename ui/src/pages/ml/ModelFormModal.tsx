import { useState } from 'react'
import { Alert, Button, Group, Modal, NumberInput, SegmentedControl, Stack, Text, TextInput, Textarea } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  MODEL_NAME_PATTERN, mlModelsKey, modelNameRule, saveModel, type MLBackend, type MLModel,
} from '@/lib/mlModels'

const splitList = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)

/**
 * Registers a model, or edits one (`model` given). A model is an address: the
 * server it is served from and its name there. The model file never passes
 * through Hermod.
 */
export function ModelFormModal({ vhost, model, onClose }: { vhost: string; model?: MLModel; onClose: () => void }) {
  const editing = model !== undefined
  const queryClient = useQueryClient()
  const [name, setName] = useState(model?.name ?? '')
  const [backend, setBackend] = useState<MLBackend>(model?.backend ?? 'oip')
  const [url, setUrl] = useState(model?.url ?? '')
  const [remoteModel, setRemoteModel] = useState(model?.remote_model ?? '')
  const [remoteVersion, setRemoteVersion] = useState(model?.remote_version ?? '')
  const [tokenSecret, setTokenSecret] = useState(model?.token_secret ?? '')
  const [inputName, setInputName] = useState(model?.input_name ?? '')
  const [features, setFeatures] = useState((model?.features ?? []).join(', '))
  const [timeoutMs, setTimeoutMs] = useState<number | string>(model?.timeout_ms ?? '')
  const [description, setDescription] = useState(model?.description ?? '')
  const [nameError, setNameError] = useState<string | null>(null)

  const save = useMutation({
    mutationFn: () =>
      saveModel(vhost, editing ? model.name : name.trim(), {
        backend,
        url: url.trim(),
        remote_model: backend === 'oip' ? remoteModel.trim() : '',
        remote_version: backend === 'oip' ? remoteVersion.trim() : '',
        token_secret: tokenSecret.trim(),
        input_name: backend === 'oip' ? inputName.trim() : '',
        features: splitList(features),
        timeout_ms: typeof timeoutMs === 'number' ? timeoutMs : 0,
        description: description.trim(),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      onClose()
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const badName = !editing && !MODEL_NAME_PATTERN.test(name.trim())
    setNameError(badName ? `"${name.trim()}" is not a valid name.` : null)
    if (badName) return
    save.mutate()
  }

  return (
    <Modal opened onClose={onClose} title={editing ? `Edit ${model.name}` : 'Add model'} centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            Workflows of {vhost} call it from a Predict node; applications call it with its serving key.
          </Text>
          {!editing && (
            <TextInput label="Name" description={modelNameRule} placeholder="churn" required value={name}
              onChange={(e) => { setName(e.currentTarget.value); setNameError(null) }} error={nameError} data-autofocus />
          )}
          <Stack gap={4}>
            <Text size="sm" fw={500}>Server protocol</Text>
            <SegmentedControl
              value={backend}
              onChange={(v) => setBackend(v as MLBackend)}
              data={[
                { value: 'oip', label: 'Open Inference (hermod-ml, KServe, Triton, MLServer)' },
                { value: 'mlflow', label: 'MLflow' },
              ]}
            />
          </Stack>
          <TextInput label="Server URL" required placeholder={backend === 'oip' ? 'http://hermod-ml:8080' : 'http://mlflow-model:5000'}
            description="The server's base address, without the protocol's path." value={url} onChange={(e) => setUrl(e.currentTarget.value)} />
          {backend === 'oip' && (
            <Group grow align="flex-start">
              <TextInput label="Model on the server" required placeholder="churn" value={remoteModel}
                onChange={(e) => setRemoteModel(e.currentTarget.value)} />
              <TextInput label="Version" description="Empty for the server's default." value={remoteVersion}
                onChange={(e) => setRemoteVersion(e.currentTarget.value)} />
            </Group>
          )}
          <TextInput label="Features" description="Comma-separated, in the order the model expects. Offered as inputs in the Predict node."
            placeholder="age, tenure, plan" value={features} onChange={(e) => setFeatures(e.currentTarget.value)} />
          {backend === 'oip' && (
            <TextInput label="Single input tensor" placeholder="input"
              description="For an exported ONNX model that takes one [rows, features] tensor: its name. Empty sends one tensor per feature."
              value={inputName} onChange={(e) => setInputName(e.currentTarget.value)} />
          )}
          <Group grow align="flex-start">
            <TextInput label="Token secret" placeholder="ML_TOKEN" description={`A secret of ${vhost}, sent as a bearer token.`}
              value={tokenSecret} onChange={(e) => setTokenSecret(e.currentTarget.value)} />
            <NumberInput label="Timeout (ms)" placeholder="30000" min={0} max={600000} value={timeoutMs} onChange={setTimeoutMs} />
          </Group>
          <Textarea label="Description" autosize minRows={2} value={description} onChange={(e) => setDescription(e.currentTarget.value)} />
          {save.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not saved">
              {(save.error as Error).message}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>Save model</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
