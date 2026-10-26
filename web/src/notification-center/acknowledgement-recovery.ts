export const acknowledgementRecoveryKey = 'myprobe-incident-ack-pending-v1'
export interface PendingAcknowledgement {
  version: 1; incidentID: string; nodeName: string; requestID: string; note: string
}
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value)
export const normalizedAcknowledgementNote = (value: string) => value.replace(/\r\n/g, '\n').trim()
export const acknowledgementNoteBytes = (value: string) => new TextEncoder().encode(normalizedAcknowledgementNote(value)).length
export function validAcknowledgementNote(value: string) {
  const normalized = normalizedAcknowledgementNote(value)
  return acknowledgementNoteBytes(value) <= 1000 && !/[\u0000-\u0008\u000B-\u001F\u007F-\u009F]/.test(normalized)
}
// Validate untrusted tab storage before rendering or posting. Return only the
// supported record shape; malformed records retain uncertainty for manual review.
export function readAcknowledgementRecovery(raw: string): PendingAcknowledgement {
  if (raw.length > (16 << 10)) throw new Error('oversized recovery record')
  const value: unknown = JSON.parse(raw)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid recovery record')
  const record = value as Record<string, unknown>
  if (record.version !== 1 || !id(record.incidentID) || !id(record.requestID) || typeof record.nodeName !== 'string' || !record.nodeName || record.nodeName.length > 1000 || typeof record.note !== 'string' || record.note.length > 4000 || !validAcknowledgementNote(record.note)) throw new Error('invalid recovery record')
  return { version: 1, incidentID: record.incidentID, nodeName: record.nodeName, requestID: record.requestID, note: record.note }
}
