import { validAcknowledgementNote } from './acknowledgement-recovery'
import type { SilenceCreateParams } from '../admin-api'

export const silenceRecoveryKey = 'myprobe-incident-silence-pending-v1'
interface PendingBase { version: 1; incidentID: string; nodeName: string; requestID: string }
export type PendingSilence = PendingBase & ({ operation: 'create'; params: SilenceCreateParams } | { operation: 'cancel'; silenceID: string; revision: number })
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v)
export function readSilenceRecovery(raw: string): PendingSilence {
  if (raw.length > (16 << 10)) throw new Error('oversized recovery record')
  const v = JSON.parse(raw)
  if (!v || typeof v !== 'object' || Array.isArray(v) || v.version !== 1 || !id(v.incidentID) || !id(v.requestID) || typeof v.nodeName !== 'string' || !v.nodeName || v.nodeName.length > 1000) throw new Error('invalid recovery record')
  const base: PendingBase = { version: 1, incidentID: v.incidentID, nodeName: v.nodeName, requestID: v.requestID }
  if (v.operation === 'cancel' && id(v.silenceID) && Number.isSafeInteger(v.revision) && v.revision > 0) return { ...base, operation: 'cancel', silenceID: v.silenceID, revision: v.revision }
  const p = v.params
  if (v.operation !== 'create' || !p || typeof p !== 'object' || Array.isArray(p) || !Number.isInteger(p.duration_seconds) || p.duration_seconds < 60 || p.duration_seconds > 604800 || typeof p.reason !== 'string' || p.reason.length > 4000 || !validAcknowledgementNote(p.reason) || typeof p.acknowledge !== 'boolean') throw new Error('invalid recovery record')
  return { ...base, operation: 'create', params: { duration_seconds: p.duration_seconds, reason: p.reason, acknowledge: p.acknowledge } }
}
