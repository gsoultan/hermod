import { describe, expect, it } from 'vitest'
import { inputProblems, whereProblems, type ConnectFields } from '@/pages/ml/connectModel'

/**
 * The Connect a model form checks what the server would refuse before it is
 * sent, and says how to fix it. Each case is one mistake a person makes.
 */

const ok: ConnectFields = {
  name: 'churn', backend: 'oip', url: 'http://kserve:8080', remoteModel: 'churn', remoteVersion: '',
  tokenSecret: '', inputName: '', features: '',
}

describe('whereProblems', () => {
  const cases: Array<{ name: string; fields: Partial<ConnectFields>; editing?: boolean; want: Record<string, RegExp> }> = [
    { name: 'a complete Open Inference model', fields: {}, want: {} },
    { name: 'an MLflow model needs no model name', fields: { backend: 'mlflow', remoteModel: '' }, want: {} },
    { name: 'no name', fields: { name: '  ' }, want: { name: /give the model a name/i } },
    { name: 'a name starting with a digit', fields: { name: '1churn' }, want: { name: /start with a letter/i } },
    { name: 'editing keeps its name', fields: { name: '' }, editing: true, want: {} },
    { name: 'no address', fields: { url: '' }, want: { url: /enter the server's address/i } },
    { name: 'an address without http', fields: { url: 'kserve:8080' }, want: { url: /start the address with http:\/\//i } },
    { name: 'an ftp address', fields: { url: 'ftp://kserve' }, want: { url: /start the address with http:\/\//i } },
    { name: 'an https address', fields: { url: 'https://kserve' }, want: {} },
    { name: 'no model on an Open Inference server', fields: { remoteModel: '' }, want: { remoteModel: /enter the model's name on the server/i } },
    { name: 'a model path', fields: { remoteModel: 'team/churn' }, want: { remoteModel: /leave out slashes/i } },
    { name: 'a version path', fields: { remoteVersion: 'v1/2' }, want: { remoteVersion: /leave out slashes/i } },
    { name: 'a complete mimir model', fields: { backend: 'mimir', remoteModel: 'm-1', tokenSecret: 'MIMIR_API_KEY' }, want: {} },
    { name: 'a mimir model needs its API key secret', fields: { backend: 'mimir', remoteModel: 'm-1' }, want: { tokenSecret: /name of the secret/i } },
    { name: 'a mimir model refuses a pasted key', fields: { backend: 'mimir', remoteModel: 'm-1', tokenSecret: 'mimir_abc.def' }, want: { tokenSecret: /name of a secret/i } },
    { name: 'a mimir model needs the model picked', fields: { backend: 'mimir', remoteModel: '', tokenSecret: 'K' }, want: { remoteModel: /pick the model/i } },
    { name: 'a mimir model id with a slash', fields: { backend: 'mimir', remoteModel: 'a/b', tokenSecret: 'K' }, want: { remoteModel: /leave out slashes/i } },
  ]
  for (const c of cases) {
    it(c.name, () => {
      const got = whereProblems({ ...ok, ...c.fields }, !!c.editing)
      expect(Object.keys(got).sort()).toEqual(Object.keys(c.want).sort())
      for (const [k, re] of Object.entries(c.want)) expect(got[k as keyof ConnectFields]).toMatch(re)
    })
  }
})

describe('inputProblems', () => {
  const cases: Array<{ name: string; fields: Partial<ConnectFields>; want: Record<string, RegExp> }> = [
    { name: 'nothing set', fields: {}, want: {} },
    { name: 'a secret name', fields: { tokenSecret: 'ML_TOKEN' }, want: {} },
    { name: 'a token pasted in place of a secret name', fields: { tokenSecret: 'abc-123.xyz' }, want: { tokenSecret: /name of a secret/i } },
    { name: 'a single input tensor without inputs', fields: { inputName: 'input' }, want: { features: /list the inputs/i } },
    { name: 'a single input tensor with inputs', fields: { inputName: 'input', features: 'age' }, want: {} },
    { name: 'MLflow ignores the tensor name', fields: { backend: 'mlflow', inputName: 'input' }, want: {} },
    { name: 'mimir ignores the tensor name', fields: { backend: 'mimir', inputName: 'input' }, want: {} },
  ]
  for (const c of cases) {
    it(c.name, () => {
      const got = inputProblems({ ...ok, ...c.fields })
      expect(Object.keys(got).sort()).toEqual(Object.keys(c.want).sort())
      for (const [k, re] of Object.entries(c.want)) expect(got[k as keyof ConnectFields]).toMatch(re)
    })
  }
})
