import type { MaintenancePreview, MaintenanceSelection } from '../admin-api'

export const maintenanceRecoveryKey = 'myprobe-maintenance-pending-v1'
export type PendingMaintenance =
  | { kind: 'create'; preview: MaintenancePreview; requestID: string }
  | { kind: 'cancel'; windowID: string; revision: number; reason: string }

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max
const ids = (value: unknown): value is string[] => Array.isArray(value) && value.length > 0 && value.length <= 100 && value.every(v => text(v, 256)) && new Set(value).size === value.length
const timestamp = (value: unknown): value is string => text(value, 64) && Number.isFinite(Date.parse(value))
function selection(value: unknown): value is MaintenanceSelection {
  if (!object(value)) return false
  if (value.kind === 'nodes') return ids(value.node_ids) && value.tags === undefined && value.tag_mode === undefined
  return value.kind === 'tags' && ids(value.tags) && value.node_ids === undefined && (value.tag_mode === 'all' || value.tag_mode === 'any')
}

// Browser storage is untrusted. Validate before assigning data used by rendering
// or a mutation; malformed records must retain uncertainty for manual review.
export function readMaintenanceRecovery(raw: string): PendingMaintenance {
  if (raw.length > (128 << 10)) throw new Error('oversized recovery record')
  const value: unknown = JSON.parse(raw)
  if (!object(value)) throw new Error('invalid recovery record')
  if (value.kind === 'cancel' && text(value.windowID, 128) && /^[A-Za-z0-9_-]+$/.test(value.windowID) && typeof value.revision === 'number' && Number.isSafeInteger(value.revision) && value.revision > 0 && text(value.reason, 1000)) {
    return value as PendingMaintenance
  }
  if (value.kind === 'create' && text(value.requestID, 128) && /^[A-Za-z0-9_-]+$/.test(value.requestID) && object(value.preview)) {
    const p = value.preview
    if (text(p.preview_digest, 64) && /^[a-f0-9]{64}$/.test(p.preview_digest) && object(p.draft) && selection(p.draft.selection) && text(p.draft.reason, 1000) && text(p.draft.timezone, 128) && timestamp(p.draft.starts_at) && timestamp(p.draft.ends_at) && Date.parse(p.draft.ends_at) > Date.parse(p.draft.starts_at) && Array.isArray(p.members) && p.members.length > 0 && p.members.length <= 100 && p.members.every(m => object(m) && text(m.id, 256) && text(m.name, 1000)) && new Set(p.members.map(m => m.id)).size === p.members.length) {
      return value as PendingMaintenance
    }
  }
  throw new Error('invalid recovery record')
}
