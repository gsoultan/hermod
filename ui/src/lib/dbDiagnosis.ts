import { requestJson } from '@/lib/executions'

/** dbdiag.Status */
export type DiagnosisStatus = 'ok' | 'warn' | 'err' | 'unknown'

export interface DiagnosisCheck {
  id: string
  title: string
  status: DiagnosisStatus
  detail: string
  value: string
}

/** One client connection; command is the statement's leading keywords only. */
export interface DiagnosisSession {
  pid: number
  user: string
  app: string
  state: string
  wait_type?: string
  wait_event?: string
  command?: string
  query_seconds: number
  xact_seconds: number
  state_seconds: number
  blocked_by?: number[]
  hidden?: boolean
  hermod: boolean
}

export interface DiagnosisReport {
  route: { through_pooler: boolean; server_port: number; dialed_port: number; server_version: string }
  checks: DiagnosisCheck[]
  hermod: DiagnosisSession[] | null
  others: DiagnosisSession[] | null
  pooler?: { readable: boolean; reason?: string; pool_size: number; cl_active: number; cl_waiting: number; sv_active: number; sv_idle: number }
  limited?: string
  checked_at: string
}

/** Reads the source database's activity; nothing on it is changed. */
export function diagnoseSource(sourceId: string): Promise<DiagnosisReport> {
  return requestJson<DiagnosisReport>(`/api/sources/${encodeURIComponent(sourceId)}/diagnosis`, { method: 'POST' })
}

/** Seconds as a person says them: 3 ms, 41 s, 48 min, 1 h 57 min. */
export function humanDuration(sec: number): string {
  if (sec < 1) return `${Math.round(sec * 1000)} ms`
  if (sec < 60) return `${Math.floor(sec)} s`
  if (sec < 3600) return `${Math.floor(sec / 60)} min`
  const h = Math.floor(sec / 3600)
  const m = Math.floor(sec / 60) % 60
  return m > 0 ? `${h} h ${m} min` : `${h} h`
}
