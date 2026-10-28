export interface ReleaseComparison {
  status: string
  current_version: string | null
  scope: 'software_version_only'
}
export interface ReleaseStatus {
  observed_at: string
  upstream: {
    status: string
    source: string
    automatic: boolean
    latest: { tag: string; url: string; published_at: string; installer_tag: boolean } | null
    checked_at: string | null
    last_success_at: string | null
    next_check_at: string | null
    error_code?: string
  }
  server: ReleaseComparison
  agent?: { node_id: string; evidence: string; hello_received_at: string | null; comparison: ReleaseComparison }
  documentation_url: string
}
