import { MODEL_NAME_PATTERN, type MLBackend } from '@/lib/mlModels'

/** What the Connect a model form holds, as typed. */
export interface ConnectFields {
  name: string
  backend: MLBackend
  url: string
  remoteModel: string
  remoteVersion: string
  tokenSecret: string
  inputName: string
  features: string
}

export type ConnectProblems = Partial<Record<keyof ConnectFields, string>>

const SECRET_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/
const hasSlash = (s: string) => /[/\\?#%]/.test(s) || s === '.' || s === '..'

function urlProblem(raw: string): string | undefined {
  const url = raw.trim()
  if (!url) return "Enter the server's address, such as http://kserve:8080."
  try {
    const u = new URL(url)
    if ((u.protocol === 'http:' || u.protocol === 'https:') && u.host) return undefined
  } catch {
    // Reported below.
  }
  return 'Start the address with http:// or https://, such as http://kserve:8080.'
}

/**
 * What stops the first step, Where it runs, from being saved, per field and
 * in words that say how to fix it. Mirrors the server's checks
 * (storage.ValidateMLModel, inference.Target.Validate) so a person hears about
 * a mistake before pressing Save. `editing` skips the name, which is fixed.
 */
export function whereProblems(f: ConnectFields, editing: boolean): ConnectProblems {
  const p: ConnectProblems = {}
  const name = f.name.trim()
  if (!editing) {
    if (!name) p.name = 'Give the model a name, such as churn.'
    else if (!MODEL_NAME_PATTERN.test(name)) {
      p.name = `"${name}" cannot be used: start with a letter, then use only letters, digits, "_" or "-".`
    }
  }
  const url = urlProblem(f.url)
  if (url) p.url = url
  if (f.backend === 'oip') {
    const model = f.remoteModel.trim()
    if (!model) p.remoteModel = "Enter the model's name on the server. It is often the same as the name above."
    else if (hasSlash(model)) p.remoteModel = "Leave out slashes: enter only the model's name, such as churn."
    const version = f.remoteVersion.trim()
    if (version && hasSlash(version)) p.remoteVersion = 'Leave out slashes: enter only the version, such as 2.'
  }
  return p
}

/** What stops the second step, What it takes, from being saved. */
export function inputProblems(f: ConnectFields): ConnectProblems {
  const p: ConnectProblems = {}
  const secret = f.tokenSecret.trim()
  if (secret && !SECRET_NAME.test(secret)) {
    p.tokenSecret = 'Enter the name of a secret, such as ML_TOKEN, not the token itself: letters, digits and "_", not starting with a digit.'
  }
  if (f.backend === 'oip' && f.inputName.trim() && !f.features.trim()) {
    p.features = 'List the inputs: a single input tensor needs them, in the order the model expects.'
  }
  return p
}
