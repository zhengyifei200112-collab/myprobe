export interface DiagnosticMetadata { preview_id: string; file_name: string; bytes: number; sha256: string; expires_at: string }
export interface DiagnosticPreview {
  preview: DiagnosticMetadata
  bundle: { format: string; generated_at: string; observation_scope: string; included: string[]; excluded: string[]; [key: string]: unknown }
}
