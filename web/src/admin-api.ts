import type { NodeMetadata, SiteSettings } from './types'
export type { SiteSettings } from './types'

export interface AdminTarget {
  id: string
  name: string
  kind: 'ping' | 'tcping'
  host: string
  port?: number
  interval_seconds: number
  timeout_ms: number
  enabled: boolean
  sort_order: number
}

export interface AdminGroup {
  id: string
  name: string
  kind: 'ping' | 'tcping'
}

export interface LatencyConfig {
  targets: AdminTarget[]
  groups: AdminGroup[]
  group_members: Array<{ group_id: string; target_id: string }>
  node_groups: Array<{ node_id: string; group_id: string }>
  node_targets: Array<{ node_id: string; target_id: string }>
}

export interface NotificationChannel {
	 delivery_paused_until?: string
  id: string
  name: string
  kind: 'webhook' | 'telegram' | 'discord' | 'smtp'
  enabled: boolean
  last_test_status?: 'success' | 'failed'
  last_test_error?: string
  last_test_at?: string
  created_at: string
  updated_at: string
}

export type AlertKind = 'offline' | 'cpu' | 'memory' | 'disk' | 'latency' | 'bandwidth' | 'cycle_traffic' | 'expiry'

export interface AlertRule {
  id: string
  policy_id?: string
  node_id: string
  channel_id: string
  kind: AlertKind
  config: {
    offline_seconds?: number
    threshold_percent?: number
    threshold_bytes_per_second?: number
    threshold_bytes?: number
    threshold_milliseconds?: number
    days_before?: number
    duration_seconds?: number
    recovery_seconds?: number
    repeat_seconds?: number
    aggregation_seconds?: number
    template_id?: string
  }
  enabled: boolean
  cooldown_seconds: number
  created_at: string
  updated_at: string
}

export interface AlertEvent {
  id: string
  rule_id: string
  node_id?: string
  state: 'firing' | 'resolved' | 'failed'
  message: string
  delivery_error?: string
  created_at: string
  delivered_at?: string
  channel_id?: string
  channel_name?: string
  provider?: string
}

export type AlertPolicyScope =
  | { kind: 'all' }
  | { kind: 'nodes'; node_ids: string[] }
  | { kind: 'tags'; tags: string[]; tag_mode: 'all' | 'any' }

export interface AlertPolicyInput {
  name: string
  policy_key: string
  enabled: boolean
  priority: number
  scope: AlertPolicyScope
  channel_id: string
  kind: AlertKind
  config: AlertRule['config']
  cooldown_seconds: number
}

export interface AlertPolicy extends AlertPolicyInput {
  id: string
  revision: number
  created_at: string
  updated_at: string
}

export interface AlertPolicyDecision {
  policy_key: string
  selected_id: string
  candidate_ids: string[]
  reason: 'only_match' | 'more_specific_scope' | 'higher_priority'
}

export interface AlertPolicyResponse {
  policy: AlertPolicy
  evaluation_enabled: boolean
  evaluation: PolicyEvaluationStatus
}

export interface PolicyEvaluationStatus {
  state: 'pending' | 'ready' | 'error'
  last_applied_at?: string
}

export const loadAlertPolicies = (after = '', limit = 50) => request<{
  policies: AlertPolicy[]; next_cursor: string; evaluation_enabled: boolean; evaluation: PolicyEvaluationStatus
}>(`/api/v1/admin/alert-policies?${new URLSearchParams({ after, limit: String(limit) })}`)
export const loadAlertPolicy = (id: string) => request<AlertPolicyResponse>(`/api/v1/admin/alert-policies/${encodeURIComponent(id)}`)
export const createAlertPolicy = (payload: AlertPolicyInput) => request<AlertPolicyResponse>('/api/v1/admin/alert-policies', { method: 'POST', body: JSON.stringify(payload) })
export const updateAlertPolicy = (id: string, payload: AlertPolicyInput & { revision: number }) => request<AlertPolicyResponse>(`/api/v1/admin/alert-policies/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) })
export const deleteAlertPolicy = (id: string, revision: number) => request<void>(`/api/v1/admin/alert-policies/${encodeURIComponent(id)}?${new URLSearchParams({ revision: String(revision) })}`, { method: 'DELETE' })
export const loadEffectiveAlertPolicies = (nodeID: string) => request<{
  decisions: AlertPolicyDecision[]; evaluation_enabled: boolean; evaluation: PolicyEvaluationStatus
}>(`/api/v1/admin/alert-policies/effective/${encodeURIComponent(nodeID)}`)

export interface NotificationTemplate {
  id: string
  name: string
  event_kind: string
  title_template: string
  body_template: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export interface ChartShare {
  id: string
  name: string
  node_ids: string[]
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ConfigImportResult {
  http_services_created?: number
  http_services_updated?: number
  nodes_created: number
  nodes_updated: number
  targets_created: number
  targets_updated: number
  groups_created: number
  groups_updated: number
  memberships_created: number
  agent_tokens?: Record<string, string>
  dry_run: boolean
}

export interface CaptchaChallenge { id: string; prompt: string; expires_at: string }
export class LoginError extends Error {
  captcha?: CaptchaChallenge
  retryAfterSeconds?: number
  constructor(message: string, captcha?: CaptchaChallenge, retryAfterSeconds?: number) { super(message); this.name = 'LoginError'; this.captcha = captcha; this.retryAfterSeconds = retryAfterSeconds }
}

export interface AuditEntry {
  id: number
  username?: string
  action: string
  object_type: string
  object_id: string
  remote_ip: string
  details: unknown
  created_at: string
}

export interface GitHubOAuthSettings {
  enabled: boolean
  client_id: string
  client_secret_set: boolean
  callback_url: string
  username_allowlist: string[]
  verified_at?: string
  updated_at: string
}

export interface AuthSettings { password_enabled: true; github: GitHubOAuthSettings }


export interface MaintenanceSelection {
  kind: 'nodes' | 'tags'
  node_ids?: string[]
  tags?: string[]
  tag_mode?: 'all' | 'any'
}
export interface MaintenanceDraft {
  selection: MaintenanceSelection
  reason: string
  timezone: string
  starts_at: string
  ends_at: string
}
export interface MaintenancePreview {
  draft: MaintenanceDraft
  members: Array<{ id: string; name: string }>
  preview_digest: string
}
export interface MaintenanceWindow {
  id: string
  revision: number
  reason: string
  timezone: string
  starts_at: string
  ends_at: string
  canceled_at?: string
  canceled_by?: string
  node_ids: string[]
  selection?: MaintenanceSelection
  created_by: string
  created_at: string
}
export interface MaintenanceWindowRef {
  id: string; reason: string; timezone: string; starts_at: string; ends_at: string; canceled_at?: string
}
export interface IncidentMaintenancePage {
  windows: Array<MaintenanceWindowRef & { active: boolean; decisions: Array<{ kind: 'observation' | 'observation_gap' | 'delivery'; first_at: string; last_at: string }> }>
  next_cursor: string; checked_at: string; node_exists: boolean; node_in_maintenance: boolean
}
export interface DeliveryMaintenancePage {
  windows: Array<MaintenanceWindowRef & { recorded_at: string }>; next_cursor: string
}
export const loadIncidentMaintenance = (id: string, after = '') => request<IncidentMaintenancePage>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/maintenance?${new URLSearchParams({ after, limit: '25' })}`)
export const loadDeliveryMaintenance = (id: string, after = '') => request<DeliveryMaintenancePage>(`/api/v1/admin/notification-deliveries/${encodeURIComponent(id)}/maintenance?${new URLSearchParams({ after, limit: '25' })}`)
export const loadMaintenanceWindows = (after = '') => request<{ windows: MaintenanceWindow[]; next_cursor: string }>(`/api/v1/admin/maintenance-windows?${new URLSearchParams({ after, limit: '25' })}`)
export const previewMaintenanceWindow = (draft: MaintenanceDraft) => request<MaintenancePreview>('/api/v1/admin/maintenance-windows/preview', { method: 'POST', body: JSON.stringify(draft) })
export const createMaintenanceWindow = (preview: MaintenancePreview, requestID: string) => request<{ window_id: string; replayed: boolean }>('/api/v1/admin/maintenance-windows', { method: 'POST', body: JSON.stringify({ draft: preview.draft, preview_digest: preview.preview_digest, request_id: requestID }) })
export const cancelMaintenanceWindow = (id: string, revision: number) => request<{ window_id: string; revision: number; canceled_at: string; replayed: boolean }>(`/api/v1/admin/maintenance-windows/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: JSON.stringify({ revision }) })

export class AdminAPIError extends Error {
  status: number
  code?: string
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'AdminAPIError'
    this.status = status
    this.code = code
  }
}
export function notificationReadError(value: unknown, subject: string): string {
  if (value instanceof AdminAPIError) {
    if (value.status === 404) return `该${subject}不存在或已被清理，请从列表重新选择。`
    if (value.status === 401) return '登录已过期，请重新登录后打开原通知入口。'
    if (value.status === 400) return '通知入口或分页参数无效，请从列表重新选择。'
  }
  return `${subject}暂时无法读取，请重试。`
}

export class AdminRequestError extends AdminAPIError {
  conflictNodeIDs: string[]
  constructor(message: string, status: number, conflictNodeIDs: string[] = [], code?: string) {
    super(message, status, code)
    this.name = 'AdminRequestError'
    this.conflictNodeIDs = conflictNodeIDs
  }
}

let csrfToken = ''

export interface HTTPServiceSpec {
  url: string
  method: 'GET' | 'HEAD'
  status_codes: number[]
  timeout_ms: number
  max_redirects: number
  max_body_bytes: number
  assertion?: { kind: 'text_contains' | 'json_equals'; text?: string; path?: string[]; expected?: unknown }
}
export interface HTTPServiceConfig {
  expected_json?: string
  id?: string
  revision: number
  name: string
  enabled: boolean
  interval_seconds: number
  node_ids: string[]
  spec: HTTPServiceSpec
}
export interface HTTPServiceSummary {
  id: string; revision: number; name: string; enabled: boolean; interval_seconds: number; node_count: number; updated_at: string
}
export interface HTTPObservation {
  task_id: string; node_id: string; revision: number; scheduled_at: string; expires_at: string
  result: null | {
    completed_at: string; outcome: 'success' | 'failure' | 'unobserved'; error_class?: string; status_code?: number; duration_ms: number
    certificates?: Array<{ sha256: string; not_before: string; not_after: string; verified: boolean }>
  }
}
export const loadHTTPObservations = (id: string) => request<{ observations: HTTPObservation[]; execution_enabled: boolean; server_time: string }>(`/api/v1/admin/service-monitors/${encodeURIComponent(id)}/results?limit=50`)
export interface HTTPStatisticsResponse {
  node_id: string; server_time: string; maintenance_excluded: boolean; execution_enabled: boolean
  statistics: {
    requested_start: string; requested_end: string; start: string; end: string; retained_from: string; schedule_known_from: string
    expected: number; success: number; failure: number; unobserved: number; missing: number; success_rate: number | null; coverage: number | null
  }
}
export const loadHTTPStatistics = (id: string, nodeID: string, start: string, end: string) => request<HTTPStatisticsResponse>(`/api/v1/admin/service-monitors/${encodeURIComponent(id)}/statistics?${new URLSearchParams({ node_id: nodeID, start, end })}`)
export const listHTTPServices = (after = '') => request<{ services: HTTPServiceSummary[]; next_cursor: string; execution_enabled: boolean }>(`/api/v1/admin/service-monitors?limit=50&after=${encodeURIComponent(after)}`)
export const loadHTTPService = (id: string) => request<{ service: HTTPServiceConfig; execution_enabled: boolean }>(`/api/v1/admin/service-monitors/${encodeURIComponent(id)}`, {}, text => {
  const result = JSON.parse(text)
  // Tokenize strings before numbers so digits inside names/URLs are untouched.
  const exact = JSON.parse(text.replace(/"(?:\\.|[^"\\])*"|(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/g, (token, number) => number === undefined ? token : JSON.stringify(number)))
  const assertion = result.service.spec.assertion
  if (assertion?.kind === 'json_equals') result.service.expected_json = typeof assertion.expected === 'number' ? exact.service.spec.assertion.expected : JSON.stringify(assertion.expected)
  return result
})
export const saveHTTPService = (value: HTTPServiceConfig) => {
  const { id, expected_json, ...body } = value
  let encoded = JSON.stringify(body)
  if (body.spec.assertion?.kind === 'json_equals' && expected_json !== undefined) {
    JSON.parse(expected_json) // Validate one JSON value before inserting its raw token.
    const { assertion, ...spec } = body.spec
    const { expected, ...condition } = assertion
    const rawAssertion = `${JSON.stringify(condition).slice(0, -1)},"expected":${expected_json}}`
    const rawSpec = `${JSON.stringify(spec).slice(0, -1)},"assertion":${rawAssertion}}`
    const { spec: omitted, ...rest } = body
    encoded = `${JSON.stringify(rest).slice(0, -1)},"spec":${rawSpec}}`
  }
  return request<{ service: HTTPServiceConfig; execution_enabled: boolean }>(`/api/v1/admin/service-monitors${id ? `/${encodeURIComponent(id)}` : ''}`, { method: id ? 'PUT' : 'POST', body: encoded })
}
export const deleteHTTPService = (id: string, revision: number) => request<void>(`/api/v1/admin/service-monitors/${encodeURIComponent(id)}?revision=${revision}`, { method: 'DELETE' })

async function request<T>(path: string, options: RequestInit = {}, decode?: (text: string) => T): Promise<T> {
  const method = (options.method || 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body !== undefined) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string; code?: string; conflict_node_ids?: string[] }
    throw new AdminRequestError(payload.error || `请求失败（${response.status}）`, response.status, payload.conflict_node_ids || [], payload.code)
  }
  if (response.status === 204) return undefined as T
  if (decode) return decode(await response.text())
  return response.json() as Promise<T>
}

export async function restoreSession(): Promise<boolean> {
  try {
    const result = await request<{ csrf_token: string }>('/api/v1/auth/me')
    csrfToken = result.csrf_token
    return true
  } catch {
    csrfToken = ''
    return false
  }
}

export async function login(username: string, password: string, captchaID = '', captchaAnswer = ''): Promise<void> {
  const response = await fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ username, password, captcha_id: captchaID, captcha_answer: captchaAnswer }), credentials: 'same-origin', cache: 'no-store' })
  const result = await response.json().catch(() => ({})) as { csrf_token?: string; error?: string; captcha?: CaptchaChallenge; retry_after_seconds?: number }
  if (!response.ok) throw new LoginError(result.error || `登录失败（${response.status}）`, result.captcha, result.retry_after_seconds)
  if (!result.csrf_token) throw new Error('登录响应缺少 CSRF Token')
  csrfToken = result.csrf_token
}

export async function logout(): Promise<void> {
  await request<void>('/api/v1/auth/logout', { method: 'POST' })
  csrfToken = ''
}

export const loadNodes = () => request<{ nodes: NodeMetadata[] }>('/api/v1/admin/nodes')
export const loadLatencyConfig = () => request<LatencyConfig>('/api/v1/admin/latency-config')
export const loadSiteSettings = () => request<{ settings: SiteSettings }>('/api/v1/admin/site-settings')
export const updateSiteSettings = (payload: SiteSettings) => request<{ settings: SiteSettings }>('/api/v1/admin/site-settings', { method: 'PATCH', body: JSON.stringify(payload) })

export const createNode = (payload: unknown) => request<{ node: NodeMetadata; agent_token: string }>('/api/v1/admin/nodes', { method: 'POST', body: JSON.stringify(payload) })
export const updateNode = (id: string, payload: unknown) => request<{ node: NodeMetadata }>(`/api/v1/admin/nodes/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteNode = (id: string) => request<void>(`/api/v1/admin/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const rotateNodeToken = (id: string) => request<{ agent_token: string }>(`/api/v1/admin/nodes/${encodeURIComponent(id)}/rotate-token`, { method: 'POST' })

export const createTarget = (payload: unknown) => request<{ target: AdminTarget }>('/api/v1/admin/targets', { method: 'POST', body: JSON.stringify(payload) })
export const updateTarget = (id: string, payload: unknown) => request<{ target: AdminTarget }>(`/api/v1/admin/targets/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteTarget = (id: string) => request<void>(`/api/v1/admin/targets/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const createGroup = (payload: unknown) => request<{ group: AdminGroup }>('/api/v1/admin/target-groups', { method: 'POST', body: JSON.stringify(payload) })
export const updateGroup = (id: string, payload: unknown) => request<{ group: AdminGroup }>(`/api/v1/admin/target-groups/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteGroup = (id: string) => request<void>(`/api/v1/admin/target-groups/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const setGroupTarget = (groupID: string, targetID: string, assigned: boolean) => request<void>(`/api/v1/admin/target-groups/${encodeURIComponent(groupID)}/targets/${encodeURIComponent(targetID)}`, { method: assigned ? 'PUT' : 'DELETE' })
export const setNodeGroup = (nodeID: string, groupID: string, assigned: boolean) => request<void>(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/target-groups/${encodeURIComponent(groupID)}`, { method: assigned ? 'PUT' : 'DELETE' })
export const setNodeTarget = (nodeID: string, targetID: string, assigned: boolean) => request<void>(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/targets/${encodeURIComponent(targetID)}`, { method: assigned ? 'PUT' : 'DELETE' })

export const loadChannels = () => request<{ channels: NotificationChannel[] }>('/api/v1/admin/notification-channels')
export const createChannel = (payload: unknown) => request<{ channel: NotificationChannel }>('/api/v1/admin/notification-channels', { method: 'POST', body: JSON.stringify(payload) })
export const updateChannel = (id: string, payload: unknown) => request<{ channel: NotificationChannel }>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteChannel = (id: string) => request<void>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const testChannel = (id: string) => request<void>(`/api/v1/admin/notification-channels/${encodeURIComponent(id)}/test`, { method: 'POST' })

export const loadAlertRules = () => request<{ rules: AlertRule[] }>('/api/v1/admin/alert-rules')
export const createAlertRule = (payload: unknown) => request<{ rule: AlertRule }>('/api/v1/admin/alert-rules', { method: 'POST', body: JSON.stringify(payload) })
export const updateAlertRule = (id: string, payload: unknown) => request<{ rule: AlertRule }>(`/api/v1/admin/alert-rules/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteAlertRule = (id: string) => request<void>(`/api/v1/admin/alert-rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const loadAlertEvents = () => request<{ events: AlertEvent[] }>('/api/v1/admin/alert-events')
export const loadNotificationTemplates = () => request<{ templates: NotificationTemplate[] }>('/api/v1/admin/notification-templates')

export type PortablePolicy = Omit<AlertPolicyInput, 'revision'> & { source_id: string }
export interface PolicyBundle { format: 'myprobe-alert-policies'; version: 1; policies: PortablePolicy[] }
export interface PolicyImportMapping { channels: Record<string, string>; nodes: Record<string, string>; templates: Record<string, string> }
export interface PolicyImportPreview { bundle: PolicyBundle; create_count: number; preview_digest: string }
export const exportPolicyBundle = () => request<PolicyBundle>('/api/v1/admin/alert-policies/export')
export const previewPolicyImport = (bundle: PolicyBundle, mapping: PolicyImportMapping) => request<PolicyImportPreview>('/api/v1/admin/alert-policies/import/preview', { method: 'POST', body: JSON.stringify({ bundle, mapping }) })
export const applyPolicyImport = (bundle: PolicyBundle, mapping: PolicyImportMapping, request_id: string, preview_digest: string) => request<{ policy_ids: string[]; replayed: boolean }>('/api/v1/admin/alert-policies/import/apply', { method: 'POST', body: JSON.stringify({ bundle, mapping, request_id, preview_digest }) })
export const createNotificationTemplate = (payload: unknown) => request<{ template: NotificationTemplate }>('/api/v1/admin/notification-templates', { method: 'POST', body: JSON.stringify(payload) })
export const updateNotificationTemplate = (id: string, payload: unknown) => request<{ template: NotificationTemplate }>(`/api/v1/admin/notification-templates/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteNotificationTemplate = (id: string) => request<void>(`/api/v1/admin/notification-templates/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const testNotificationTemplate = (id: string, channelID: string) => request<void>(`/api/v1/admin/notification-templates/${encodeURIComponent(id)}/test`, { method: 'POST', body: JSON.stringify({ channel_id: channelID }) })

export const loadChartShares = () => request<{ shares: ChartShare[] }>('/api/v1/admin/chart-shares')
export const createChartShare = (payload: unknown) => request<{ share: ChartShare; path: string }>('/api/v1/admin/chart-shares', { method: 'POST', body: JSON.stringify(payload) })
export const updateChartShare = (id: string, payload: unknown) => request<{ share: ChartShare; path: string }>(`/api/v1/admin/chart-shares/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const deleteChartShare = (id: string) => request<void>(`/api/v1/admin/chart-shares/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const changePassword = (currentPassword: string, newPassword: string) => request<void>('/api/v1/auth/password', { method: 'POST', body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }) })
export const loadGitHubStatus = () => request<{ enabled: boolean }>('/api/v1/auth/github/status')
export const loadAuthSettings = () => request<AuthSettings>('/api/v1/admin/auth-settings')
export const updateAuthSettings = (payload: unknown) => request<AuthSettings>('/api/v1/admin/auth-settings', { method: 'PATCH', body: JSON.stringify(payload) })
export const loadAudit = (beforeID?: number) => request<{ entries: AuditEntry[]; next_before_id?: number }>(`/api/v1/admin/audit?limit=50${beforeID ? `&before_id=${beforeID}` : ''}`)

async function downloadRequest(path: string, options: RequestInit = {}): Promise<{ blob: Blob; filename: string }> {
  const headers = new Headers(options.headers)
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  const disposition = response.headers.get('Content-Disposition') || ''
  const filename = disposition.match(/filename="([^"]+)"/)?.[1] || 'myprobe-download'
  return { blob: await response.blob(), filename }
}

export const downloadConfiguration = () => downloadRequest('/api/v1/admin/maintenance/config')
export const importConfiguration = (config: unknown, dryRun: boolean) => request<{ result: ConfigImportResult }>('/api/v1/admin/maintenance/config/import', { method: 'POST', body: JSON.stringify({ dry_run: dryRun, config }) })
export const downloadDatabaseBackup = (passphrase: string) => downloadRequest('/api/v1/admin/maintenance/backup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ passphrase }) })

export async function uploadDatabaseRestore(file: File, passphrase: string): Promise<{ staged: boolean; restart_required: boolean }> {
  const form = new FormData()
  form.set('file', file)
  form.set('passphrase', passphrase)
  const headers = new Headers()
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch('/api/v1/admin/maintenance/restore', { method: 'POST', body: form, headers, credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  return response.json()
}

export interface NodeBatchRequest {
  node_ids: string[]
  add_tags?: string[]
  remove_tags?: string[]
  hidden?: boolean
  collection_seconds?: number
  report_seconds?: number
  targets?: { mode: 'add' | 'remove' | 'replace'; ids: string[] }
}
export interface BatchNodeState { tags: string[]; hidden: boolean; collection_seconds: number; report_seconds: number; target_ids: string[] }
export interface NodeBatchPreview {
  id: string; expires_at: string
  nodes: Array<{ node_id: string; name: string; revision: number; changed: boolean; before: BatchNodeState; after: BatchNodeState }>
}
export interface NodeBatchResult { preview_id: string; applied_at: string; changed_ids: string[]; unchanged_ids: string[] }
export const previewNodeBatch = (payload: NodeBatchRequest) => request<NodeBatchPreview>('/api/v1/admin/nodes/batch/preview', { method: 'POST', body: JSON.stringify(payload) })
export const applyNodeBatch = (previewID: string, key: string) => request<NodeBatchResult>('/api/v1/admin/nodes/batch/apply', { method: 'POST', body: JSON.stringify({ preview_id: previewID, idempotency_key: key }) })

export interface NodeOrderEntry { id: string; sort_order: number }
export const reorderNodes = (expected: NodeOrderEntry[], nodeIDs: string[]) => request<{ changed: boolean }>('/api/v1/admin/nodes/reorder', { method: 'POST', body: JSON.stringify({ expected, node_ids: nodeIDs }) })

export interface Incident {
  seq: number; id: string; node_id: string; node_name: string; kind: string
  state: 'pending' | 'firing' | 'resolved'; started_at: string; resolved_at?: string
  observation_stale: boolean; message: string; origin: string; approximate_start: boolean; resolution_reason?: string
  acknowledgement?: { actor_name: string; acknowledged_at: string }
}
export interface IncidentAcknowledgement {
  incident_id: string; actor_id: string; actor_name: string; acknowledged_at: string; note: string
}
export interface IncidentAcknowledgementView {
  incident_state: Incident['state']; acknowledgement: IncidentAcknowledgement | null
}
export const loadIncidentAcknowledgement = (id: string) => request<IncidentAcknowledgementView>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/acknowledgement`)
export const acknowledgeIncident = (id: string, requestID: string, note: string) => request<{ acknowledgement: IncidentAcknowledgement; replayed: boolean }>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/acknowledgement`, { method: 'POST', body: JSON.stringify({ request_id: requestID, note }) })
export interface IncidentDelivery {
	 attempt_sequence?: number; delivery_paused_until?: string
  seq: number; id: string; channel_name: string; provider: string
  notification_type: string; status: string; attempt_count: number
  created_at: string; available_at: string; error_class?: string
  incident_id: string; aggregate_id?: string
}
export const loadIncident = (id: string) => request<{ incident: Incident }>(`/api/v1/admin/incidents/${encodeURIComponent(id)}`)

export interface AggregateTotals { pending: number; inflight: number; delivered: number; failed: number; canceled: number }
export interface NotificationAggregate {
	 delivery_paused_until?: string
  seq: number; id: string; policy_id: string; policy_revision: number; channel_id: string; provider: string
  notification_type: string; starts_at: string; ends_at: string; status: string; sealed: boolean; attempt_count: number; available_at: string
}
export interface AggregateContent {
  title: string; message: string; state: string; timestamp: string; snapshot_id?: string; group_path?: string
  members: { incident_id: string; node_id: string; node_name: string; event_path: string; observed_at: string }[]
}
export interface AggregateAttempt {
  number: number; started_at: string; completed_at?: string; outcome: string; error_class?: string; content: AggregateContent | null
  members: { delivery_id: string; incident_id: string; node_name: string; delivery_attempt: number; included: boolean }[]
}
export interface AggregateContext {
  aggregate: NotificationAggregate; checked_at: string; totals: AggregateTotals; next_before: number
  members: (IncidentDelivery & { node_name: string; incident_state: string; observation_stale: boolean })[]
  attempts: AggregateAttempt[]
}
export const loadNotificationAggregates = (before = 0) => request<{ aggregates: (NotificationAggregate & { channel_name: string; totals: AggregateTotals })[]; checked_at: string; next_before: number }>(`/api/v1/admin/notification-aggregates?limit=25&before=${before}`)
export const loadNotificationAggregate = (id: string, before = 0) => request<AggregateContext>(`/api/v1/admin/notification-aggregates/${encodeURIComponent(id)}?limit=25&before=${before}`)
export const loadIncidents = (state = '', before = 0, nodeID = '') => request<{ incidents: Incident[]; next_before: number }>(`/api/v1/admin/incidents?limit=25&state=${encodeURIComponent(state)}&before=${before}&node_id=${encodeURIComponent(nodeID)}`)
export const loadIncidentDeliveries = (id: string, before = 0) => request<{ deliveries: IncidentDelivery[]; next_before: number }>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/deliveries?limit=25&before=${before}`)

export interface DeliveryAttempt { number: number; started_at: string; completed_at?: string; outcome: string; error_class?: string }
export const loadDeliveryAttempts = (id: string, before = 0) => request<{ attempts: DeliveryAttempt[]; next_before: number }>(`/api/v1/admin/notification-deliveries/${encodeURIComponent(id)}/attempts?limit=25&before=${before}`)

export interface IncidentSilence {
  id: string; incident_id: string; revision: number; starts_at: string; ends_at: string
  reason: string; created_by: string; creator_name: string; acknowledge_requested: boolean
  canceled_at?: string; canceled_by?: string; canceler_name?: string; closed_at?: string; closure_reason?: string
}
export interface SilenceRecord extends IncidentSilence {
  active: boolean; recorded_at?: string; decisions: { kind: string; first_at: string; last_at: string }[]
}
export interface SilencePage {
  incident_state?: Incident['state']; checked_at: string; active_silence: IncidentSilence | null
  silences: SilenceRecord[]; next_cursor: string
}
export interface SilenceCreateParams { duration_seconds: number; reason: string; acknowledge: boolean }
export interface SilenceMutationResult { silence_id: string; revision: number; starts_at: string; ends_at: string; canceled_at?: string; acknowledgement_created: boolean; replayed: boolean }
export const loadIncidentSilences = (id: string, after = '') => request<SilencePage>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/silences?limit=25&after=${encodeURIComponent(after)}`)
export const loadDeliverySilences = (id: string, after = '') => request<SilencePage>(`/api/v1/admin/notification-deliveries/${encodeURIComponent(id)}/silences?limit=25&after=${encodeURIComponent(after)}`)
export const createIncidentSilence = (id: string, requestID: string, params: SilenceCreateParams) => request<SilenceMutationResult>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/silences`, { method: 'POST', body: JSON.stringify({ request_id: requestID, ...params }) })
export const cancelIncidentSilence = (id: string, silenceID: string, requestID: string, revision: number) => request<SilenceMutationResult>(`/api/v1/admin/incidents/${encodeURIComponent(id)}/silences/${encodeURIComponent(silenceID)}/cancel`, { method: 'POST', body: JSON.stringify({ request_id: requestID, revision }) })
