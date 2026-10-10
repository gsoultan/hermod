import { Text } from '@mantine/core'
import { describeRetrain, isNever, type MLModel } from '@/lib/mlModels'

/** When the model retrains and how the last retraining went, in a line each. */
export function RetrainStatusLine({ model }: { model: MLModel }) {
  const p = model.retrain
  const st = model.retrain_status
  if (!p) return null
  const ran = st && !isNever(st.at)
  return (
    <>
      <Text size="xs" c="dimmed">{describeRetrain(p)}</Text>
      {ran && (st.error ? (
        <Text size="xs" c="red">Last retrain failed: {st.error}</Text>
      ) : (
        <Text size="xs" c={st.live ? 'teal' : 'dimmed'} title={st.reason}>
          Last retrain: version {st.version}, {st.live ? 'live' : 'not live'}
        </Text>
      ))}
    </>
  )
}
