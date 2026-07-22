<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import type { NodeMetadata } from './types'
import type { AdminGroup, AdminTarget, AlertEvent, AlertKind, AlertRule, AuditEntry, ChartShare, ConfigImportResult, LatencyConfig, NotificationChannel } from './admin-api'
import {
  changePassword, createAlertRule, createChannel, createChartShare, createGroup, createNode, createTarget, deleteAlertRule, deleteChannel, deleteChartShare,
  deleteGroup, deleteNode, deleteTarget, loadAlertEvents, loadAlertRules, loadChannels, loadLatencyConfig,
  downloadConfiguration, downloadDatabaseBackup, importConfiguration, loadAudit, loadChartShares, loadNodes, login, LoginError, logout, restoreSession, rotateNodeToken, setGroupTarget, setNodeGroup, testChannel, uploadDatabaseRestore,
  updateAlertRule, updateChannel, updateChartShare, updateGroup, updateNode, updateTarget,
} from './admin-api'

type Tab = 'nodes' | 'targets' | 'groups' | 'alerts' | 'shares' | 'maintenance' | 'security'
const authenticated = ref(false)
const booting = ref(true)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const tab = ref<Tab>('nodes')
const username = ref('admin')
const password = ref('')
const captchaID = ref('')
const captchaPrompt = ref('')
const captchaAnswer = ref('')
const nodes = ref<NodeMetadata[]>([])
const config = ref<LatencyConfig>({ targets: [], groups: [], group_members: [], node_groups: [] })
const channels = ref<NotificationChannel[]>([])
const rules = ref<AlertRule[]>([])
const events = ref<AlertEvent[]>([])
const shares = ref<ChartShare[]>([])
const token = ref('')
const tokenNode = ref('')
const configFile = ref<File | null>(null)
const configDocument = ref<unknown>(null)
const configPreview = ref<ConfigImportResult | null>(null)
const backupPassphrase = ref('')
const restorePassphrase = ref('')
const restoreFile = ref<File | null>(null)
const importTokens = ref<Record<string, string>>({})
const auditEntries = ref<AuditEntry[]>([])
const nextAuditID = ref<number | undefined>()
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')

const emptyNode = () => ({ name: '', tags: '', country_code: '', collection_seconds: 5, report_seconds: 5 })
const nodeCreate = reactive(emptyNode())
const nodeEdit = ref<NodeMetadata | null>(null)
const customEdit = ref<NodeMetadata | null>(null)
const emptyTarget = (): Omit<AdminTarget, 'id'> => ({ name: '', kind: 'ping', host: '', interval_seconds: 60, timeout_ms: 3000, enabled: true, sort_order: 0 })
const targetForm = reactive(emptyTarget() as Omit<AdminTarget, 'id'> & { id?: string })
const groupForm = reactive({ id: '', name: '', kind: 'ping' as 'ping' | 'tcping' })
const emptyChannel = () => ({ id: '', name: '', kind: 'webhook' as 'webhook' | 'telegram', url: '', bot_token: '', chat_id: '', enabled: true })
const channelForm = reactive(emptyChannel())
const emptyRule = () => ({ id: '', node_id: '', channel_id: '', kind: 'offline' as AlertKind, threshold: 60, cooldown_seconds: 900, enabled: true })
const ruleForm = reactive(emptyRule())
const emptyShare = () => ({ id: '', name: '', password: '', node_ids: [] as string[], enabled: true })
const shareForm = reactive(emptyShare())

function showError(value: unknown) {
  error.value = value instanceof Error ? value.message : '操作失败'
}

async function refresh() {
  const [nodeResult, latencyResult, channelResult, ruleResult, eventResult, shareResult, auditResult] = await Promise.all([
    loadNodes(), loadLatencyConfig(), loadChannels(), loadAlertRules(), loadAlertEvents(), loadChartShares(), loadAudit(),
  ])
  nodes.value = nodeResult.nodes
  config.value = latencyResult
  channels.value = channelResult.channels
  rules.value = ruleResult.rules
  events.value = eventResult.events
  shares.value = shareResult.shares
  auditEntries.value = auditResult.entries
  nextAuditID.value = auditResult.next_before_id
}

async function run(action: () => Promise<void>, success = '') {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await action()
    if (success) notice.value = success
  } catch (value) {
    showError(value)
  } finally {
    busy.value = false
  }
}

async function submitLogin() {
  await run(async () => {
    try {
      await login(username.value.trim(), password.value, captchaID.value, captchaAnswer.value)
    } catch (value) {
      if (value instanceof LoginError && value.captcha) {
        captchaID.value = value.captcha.id
        captchaPrompt.value = value.captcha.prompt
        captchaAnswer.value = ''
      } else if (value instanceof LoginError && value.retryAfterSeconds) {
        captchaID.value = ''
        captchaPrompt.value = ''
        captchaAnswer.value = ''
      }
      throw value
    }
    password.value = ''
    captchaID.value = ''; captchaPrompt.value = ''; captchaAnswer.value = ''
    authenticated.value = true
    await refresh()
  })
}

async function submitPasswordChange() {
  if (newPassword.value !== confirmPassword.value) { error.value = '两次输入的新密码不一致。'; return }
  await run(async () => {
    await changePassword(currentPassword.value, newPassword.value)
    currentPassword.value = ''; newPassword.value = ''; confirmPassword.value = ''
    authenticated.value = false
  }, '密码已修改，所有会话均已撤销，请重新登录。')
}

async function loadMoreAudit() {
  if (!nextAuditID.value) return
  await run(async () => {
    const result = await loadAudit(nextAuditID.value)
    auditEntries.value.push(...result.entries)
    nextAuditID.value = result.next_before_id
  })
}

function auditLabel(item: AuditEntry) {
  const labels: Record<string, string> = { create:'创建', update:'更新', delete:'删除', rotate_token:'轮换 Token', test:'测试', import:'导入', export:'导出', stage_restore:'暂存恢复', change_password:'修改密码', attach_target:'添加目标', detach_target:'移除目标', assign_group:'分配组', unassign_group:'取消分组' }
  return labels[item.action] || item.action
}

async function signOut() {
  await run(async () => {
    await logout()
    authenticated.value = false
    nodes.value = []
  })
}

async function submitNodeCreate() {
  await run(async () => {
    const result = await createNode({ ...nodeCreate, tags: nodeCreate.tags.split(',').map(x => x.trim()).filter(Boolean) })
    token.value = result.agent_token
    tokenNode.value = result.node.name
    Object.assign(nodeCreate, emptyNode())
    await refresh()
  }, '节点已创建，请立即保存 Agent Token。')
}

function editNode(item: NodeMetadata) {
  const copy = JSON.parse(JSON.stringify(item)) as NodeMetadata
  copy.custom_badges ||= []
  copy.custom_links ||= []
  if (copy.expires_at) {
    const date = new Date(copy.expires_at)
    const offset = date.getTimezoneOffset() * 60_000
    copy.expires_at = new Date(date.getTime() - offset).toISOString().slice(0, 16)
  }
  nodeEdit.value = copy
}

function addCustomBadge() {
  customEdit.value?.custom_badges?.push({ label: '', color: 'blue' })
}

function addCustomLink() {
  customEdit.value?.custom_links?.push({ label: '', url: 'https://' })
}

function editCustomDisplay(item: NodeMetadata) {
  const copy = JSON.parse(JSON.stringify(item)) as NodeMetadata
  copy.custom_badges ||= []
  copy.custom_links ||= []
  customEdit.value = copy
}

async function saveCustomDisplay() {
  if (!customEdit.value) return
  const item = customEdit.value
  await run(async () => {
    await updateNode(item.id, {
      ...item,
      price_minor: typeof item.price_minor === 'number' ? item.price_minor : null,
      expires_at: item.expires_at || null,
      traffic_reset_day: item.traffic_reset_day || null,
    })
    customEdit.value = null
    await refresh()
  }, '节点自定义展示已清洗并保存。')
}

async function saveNode() {
  if (!nodeEdit.value) return
  const item = nodeEdit.value
  await run(async () => {
    await updateNode(item.id, {
      ...item,
      price_minor: typeof item.price_minor === 'number' ? item.price_minor : null,
      expires_at: item.expires_at ? new Date(item.expires_at).toISOString() : null,
      traffic_reset_day: item.traffic_reset_day || null,
    })
    nodeEdit.value = null
    await refresh()
  }, '节点配置已保存。')
}

async function removeNode(item: NodeMetadata) {
  if (!confirm(`确认删除节点“${item.name}”？相关历史数据也会删除。`)) return
  await run(async () => { await deleteNode(item.id); await refresh() }, '节点已删除。')
}

async function rotateToken(item: NodeMetadata) {
  if (!confirm(`确认轮换“${item.name}”的 Agent Token？旧 Token 会立即失效。`)) return
  await run(async () => {
    const result = await rotateNodeToken(item.id)
    token.value = result.agent_token
    tokenNode.value = item.name
  }, 'Token 已轮换，请立即更新 Agent。')
}

function editTarget(item?: AdminTarget) {
  Object.assign(targetForm, item ? { ...item } : emptyTarget(), { id: item?.id })
}

async function saveTarget() {
  await run(async () => {
    const payload = { ...targetForm, port: targetForm.kind === 'tcping' ? Number(targetForm.port || 0) : null }
    if (targetForm.id) await updateTarget(targetForm.id, payload)
    else await createTarget(payload)
    editTarget()
    await refresh()
  }, targetForm.id ? '探测目标已更新。' : '探测目标已创建。')
}

async function removeTarget(item: AdminTarget) {
  if (!confirm(`确认删除探测目标“${item.name}”？`)) return
  await run(async () => { await deleteTarget(item.id); await refresh() }, '探测目标已删除。')
}

function editGroup(item?: AdminGroup) {
  Object.assign(groupForm, item ? { ...item } : { id: '', name: '', kind: 'ping' })
}

async function saveGroup() {
  await run(async () => {
    const payload = { name: groupForm.name, kind: groupForm.kind }
    if (groupForm.id) await updateGroup(groupForm.id, payload)
    else await createGroup(payload)
    editGroup()
    await refresh()
  }, groupForm.id ? '目标组已更新。' : '目标组已创建。')
}

async function removeGroup(item: AdminGroup) {
  if (!confirm(`确认删除目标组“${item.name}”？`)) return
  await run(async () => { await deleteGroup(item.id); await refresh() }, '目标组已删除。')
}

function isGroupTarget(groupID: string, targetID: string) {
  return config.value.group_members.some(item => item.group_id === groupID && item.target_id === targetID)
}

function isNodeGroup(nodeID: string, groupID: string) {
  return config.value.node_groups.some(item => item.node_id === nodeID && item.group_id === groupID)
}

async function toggleGroupTarget(group: AdminGroup, target: AdminTarget, assigned: boolean) {
  await run(async () => { await setGroupTarget(group.id, target.id, assigned); await refresh() }, assigned ? '目标已加入分组。' : '目标已移出分组。')
}

async function toggleNodeGroup(node: NodeMetadata, group: AdminGroup, assigned: boolean) {
  await run(async () => { await setNodeGroup(node.id, group.id, assigned); await refresh() }, assigned ? '目标组已分配。' : '目标组已取消。')
}

function editChannel(item?: NotificationChannel) {
  Object.assign(channelForm, emptyChannel(), item ? { id: item.id, name: item.name, kind: item.kind, enabled: item.enabled } : {})
}

async function saveChannel() {
  const credentialConfig = channelForm.kind === 'webhook'
    ? (channelForm.url ? { url: channelForm.url } : undefined)
    : (channelForm.bot_token || channelForm.chat_id ? { bot_token: channelForm.bot_token, chat_id: channelForm.chat_id } : undefined)
  await run(async () => {
    const payload = { name: channelForm.name, kind: channelForm.kind, enabled: channelForm.enabled, config: credentialConfig }
    if (channelForm.id) await updateChannel(channelForm.id, payload)
    else await createChannel(payload)
    editChannel()
    await refresh()
  }, channelForm.id ? '通知通道已更新。' : '通知通道已创建。')
}

async function removeChannel(item: NotificationChannel) {
  if (!confirm(`确认删除通知通道“${item.name}”？关联告警规则也会删除。`)) return
  await run(async () => { await deleteChannel(item.id); await refresh() }, '通知通道已删除。')
}

async function sendChannelTest(item: NotificationChannel) {
  await run(async () => { await testChannel(item.id) }, '测试通知已发送。')
}

function thresholdLabel(kind: AlertKind) {
  return ({ offline: '离线秒数', cpu: 'CPU 百分比', bandwidth: '总带宽 MiB/s', cycle_traffic: '周期流量 GiB', expiry: '提前天数' } as Record<AlertKind, string>)[kind]
}

function ruleConfig(kind: AlertKind, threshold: number) {
  if (kind === 'offline') return { offline_seconds: threshold }
  if (kind === 'cpu') return { threshold_percent: threshold }
  if (kind === 'bandwidth') return { threshold_bytes_per_second: Math.round(threshold * 1024 * 1024) }
  if (kind === 'cycle_traffic') return { threshold_bytes: Math.round(threshold * 1024 * 1024 * 1024) }
  return { days_before: threshold }
}

function editRule(item?: AlertRule) {
  if (!item) { Object.assign(ruleForm, emptyRule()); return }
  let threshold = item.config.offline_seconds ?? item.config.threshold_percent ?? item.config.days_before ?? 0
  if (item.kind === 'bandwidth') threshold = (item.config.threshold_bytes_per_second || 0) / 1024 / 1024
  if (item.kind === 'cycle_traffic') threshold = (item.config.threshold_bytes || 0) / 1024 / 1024 / 1024
  Object.assign(ruleForm, { id: item.id, node_id: item.node_id, channel_id: item.channel_id, kind: item.kind, threshold, cooldown_seconds: item.cooldown_seconds, enabled: item.enabled })
}

async function saveRule() {
  await run(async () => {
    const payload = { node_id: ruleForm.node_id, channel_id: ruleForm.channel_id, kind: ruleForm.kind, config: ruleConfig(ruleForm.kind, ruleForm.threshold), cooldown_seconds: ruleForm.cooldown_seconds, enabled: ruleForm.enabled }
    if (ruleForm.id) await updateAlertRule(ruleForm.id, payload)
    else await createAlertRule(payload)
    editRule()
    await refresh()
  }, ruleForm.id ? '告警规则已更新。' : '告警规则已创建。')
}

async function removeRule(item: AlertRule) {
  if (!confirm('确认删除此告警规则？')) return
  await run(async () => { await deleteAlertRule(item.id); await refresh() }, '告警规则已删除。')
}

function nodeName(id?: string) { return nodes.value.find(item => item.id === id)?.name || id || '未知节点' }
function channelName(id: string) { return channels.value.find(item => item.id === id)?.name || id }
function kindName(kind: AlertKind) { return ({ offline: '离线', cpu: 'CPU', bandwidth: '带宽', cycle_traffic: '周期流量', expiry: '到期' } as Record<AlertKind, string>)[kind] }

function editShare(item?: ChartShare) {
  Object.assign(shareForm, emptyShare(), item ? { id: item.id, name: item.name, node_ids: [...item.node_ids], enabled: item.enabled } : {})
}

async function saveShare() {
  await run(async () => {
    const payload: Record<string, unknown> = { name: shareForm.name, node_ids: shareForm.node_ids, enabled: shareForm.enabled }
    if (!shareForm.id || shareForm.password) payload.password = shareForm.password
    if (shareForm.id) await updateChartShare(shareForm.id, payload)
    else await createChartShare(payload)
    editShare()
    await refresh()
  }, shareForm.id ? '分享配置已更新。' : '分享链接已创建。')
}

async function removeShare(item: ChartShare) {
  if (!confirm(`确认删除分享“${item.name}”？所有访客会话将立即失效。`)) return
  await run(async () => { await deleteChartShare(item.id); await refresh() }, '分享链接已删除。')
}

async function copyShare(item: ChartShare) {
  try {
    await navigator.clipboard.writeText(`${location.origin}/share/${item.id}`)
    notice.value = '分享链接已复制。'
  } catch {
    error.value = '浏览器拒绝访问剪贴板，请从地址栏手动复制分享链接。'
  }
}

async function copyToken() {
  await navigator.clipboard.writeText(token.value)
  notice.value = 'Token 已复制。'
}

function saveDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

async function exportConfigFile() {
  await run(async () => {
    const result = await downloadConfiguration()
    saveDownload(result.blob, result.filename)
  }, '配置文件已导出；其中不包含密码、Token、通知凭据和历史数据。')
}

function selectConfigFile(event: Event) {
  configFile.value = (event.target as HTMLInputElement).files?.[0] || null
  configDocument.value = null
  configPreview.value = null
}

async function previewConfigImport() {
  if (!configFile.value) return
  await run(async () => {
    if (configFile.value!.size > 10 * 1024 * 1024) throw new Error('配置文件不能超过 10 MiB。')
    configDocument.value = JSON.parse(await configFile.value!.text())
    configPreview.value = (await importConfiguration(configDocument.value, true)).result
  }, '预检通过；尚未修改任何配置。')
}

async function applyConfigImport() {
  if (!configDocument.value || !configPreview.value) return
  if (!confirm('确认以合并模式导入此配置？同 ID 项会更新，未出现在文件中的现有项会保留。')) return
  await run(async () => {
    const result = (await importConfiguration(configDocument.value, false)).result
    importTokens.value = result.agent_tokens || {}
    configPreview.value = null
    configDocument.value = null
    configFile.value = null
    await refresh()
  }, '配置已合并导入。新节点 Token 只在当前弹窗显示一次。')
}

async function exportEncryptedBackup() {
  await run(async () => {
    if (backupPassphrase.value.length < 12) throw new Error('备份口令至少需要 12 个字符。')
    const result = await downloadDatabaseBackup(backupPassphrase.value)
    saveDownload(result.blob, result.filename)
    backupPassphrase.value = ''
  }, '加密数据库备份已下载。请将口令与备份文件分开保存。')
}

function selectRestoreFile(event: Event) {
  restoreFile.value = (event.target as HTMLInputElement).files?.[0] || null
}

async function stageRestore() {
  if (!restoreFile.value) return
  if (!confirm('确认校验并暂存此数据库备份？当前服务不会立即切换，重启后才会启用，并保留恢复前数据库。')) return
  await run(async () => {
    if (restorePassphrase.value.length < 12) throw new Error('恢复口令至少需要 12 个字符。')
    await uploadDatabaseRestore(restoreFile.value!, restorePassphrase.value)
    restorePassphrase.value = ''
    restoreFile.value = null
  }, '备份校验成功并已暂存。请在合适的维护窗口重启服务以完成恢复。')
}

onMounted(async () => {
  authenticated.value = await restoreSession()
  if (authenticated.value) {
    try { await refresh() } catch (value) { showError(value) }
  }
  booting.value = false
})
</script>

<template>
  <div class="admin-shell">
    <header class="admin-nav">
      <a class="brand" href="/"><span class="brand-mark">MP</span><span>MyProbe <small>管理中心</small></span></a>
      <nav v-if="authenticated" class="admin-tabs">
        <button :class="{ active: tab === 'nodes' }" @click="tab = 'nodes'">节点</button>
        <button :class="{ active: tab === 'targets' }" @click="tab = 'targets'">探测目标</button>
        <button :class="{ active: tab === 'groups' }" @click="tab = 'groups'">目标组</button>
        <button :class="{ active: tab === 'alerts' }" @click="tab = 'alerts'">告警</button>
        <button :class="{ active: tab === 'shares' }" @click="tab = 'shares'">分享</button>
        <button :class="{ active: tab === 'maintenance' }" @click="tab = 'maintenance'">维护</button>
        <button :class="{ active: tab === 'security' }" @click="tab = 'security'">安全</button>
      </nav>
      <div class="nav-actions"><a class="soft-button" href="/">公开面板</a><button v-if="authenticated" class="soft-button" @click="signOut">退出</button></div>
    </header>

    <main v-if="booting" class="state-panel"><div class="loader"></div><p>正在恢复管理会话…</p></main>
    <main v-else-if="!authenticated" class="login-wrap">
      <form class="admin-panel login-card" @submit.prevent="submitLogin">
        <span class="eyebrow">SECURE CONSOLE</span><h1>登录管理中心</h1><p>使用初始化时配置的管理员账号登录。</p>
        <label>用户名<input v-model="username" autocomplete="username" required></label>
        <label>密码<input v-model="password" type="password" autocomplete="current-password" required></label>
        <label v-if="captchaPrompt">安全验证：{{ captchaPrompt }}<input v-model="captchaAnswer" inputmode="numeric" autocomplete="off" required></label>
        <p v-if="notice" class="form-message success">{{ notice }}</p>
        <p v-if="error" class="form-message error">{{ error }}</p>
        <button class="primary-button" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
      </form>
    </main>

    <main v-else class="admin-main">
      <div v-if="error" class="admin-alert error">{{ error }}</div><div v-if="notice" class="admin-alert success">{{ notice }}</div>

      <template v-if="tab === 'nodes'">
        <section class="admin-heading"><div><span class="eyebrow">INFRASTRUCTURE</span><h1>节点管理</h1><p>创建 Agent 身份、调整公开展示与采集策略，并分配延迟探测组。</p></div><span class="count-pill">{{ nodes.length }} 个节点</span></section>
        <form class="admin-panel compact-form" @submit.prevent="submitNodeCreate">
          <h2>添加节点</h2><div class="form-grid four"><label>名称<input v-model="nodeCreate.name" required></label><label>标签（逗号分隔）<input v-model="nodeCreate.tags" placeholder="香港, 生产"></label><label>国家/地区代码<input v-model="nodeCreate.country_code" maxlength="2" placeholder="HK"></label><label>上报间隔（秒）<input v-model.number="nodeCreate.report_seconds" type="number" min="1" max="3600" required></label></div>
          <button class="primary-button" :disabled="busy">创建节点</button>
        </form>
        <section class="admin-list">
          <article v-for="item in nodes" :key="item.id" class="admin-panel entity-card">
            <div class="entity-title"><div><strong>{{ item.name }}</strong><code>{{ item.id }}</code></div><span :class="['status-label', item.hidden ? 'muted' : 'active']">{{ item.hidden ? '已隐藏' : '公开' }}</span></div>
            <div class="entity-meta"><span>{{ item.country_code || '未设置地区' }}</span><span>采集 {{ item.collection_seconds }}s / 上报 {{ item.report_seconds }}s</span><span>{{ item.latency_mode.toUpperCase() }}</span></div>
            <div class="assignment-box"><b>分配目标组</b><label v-for="group in config.groups" :key="group.id" class="check-chip"><input type="checkbox" :checked="isNodeGroup(item.id, group.id)" :disabled="busy" @change="toggleNodeGroup(item, group, ($event.target as HTMLInputElement).checked)">{{ group.name }}</label><span v-if="!config.groups.length" class="empty-inline">请先创建目标组</span></div>
            <div class="entity-actions"><button @click="editNode(item)">编辑</button><button @click="editCustomDisplay(item)">自定义展示</button><button @click="rotateToken(item)">轮换 Token</button><button class="danger" @click="removeNode(item)">删除</button></div>
          </article>
        </section>
      </template>

      <template v-else-if="tab === 'targets'">
        <section class="admin-heading"><div><span class="eyebrow">LATENCY PROBES</span><h1>探测目标</h1><p>配置由 Agent 执行的 ICMP Ping 或 TCPing 目标。</p></div><span class="count-pill">{{ config.targets.length }} 个目标</span></section>
        <form class="admin-panel compact-form" @submit.prevent="saveTarget">
          <h2>{{ targetForm.id ? '编辑目标' : '添加目标' }}</h2><div class="form-grid six"><label>名称<input v-model="targetForm.name" required></label><label>类型<select v-model="targetForm.kind"><option value="ping">Ping</option><option value="tcping">TCPing</option></select></label><label>主机<input v-model="targetForm.host" required placeholder="example.com"></label><label>端口<input v-model.number="targetForm.port" type="number" min="1" max="65535" :required="targetForm.kind === 'tcping'" :disabled="targetForm.kind === 'ping'"></label><label>间隔（秒）<input v-model.number="targetForm.interval_seconds" type="number" min="5" max="86400"></label><label>超时（毫秒）<input v-model.number="targetForm.timeout_ms" type="number" min="1"></label><label>排序<input v-model.number="targetForm.sort_order" type="number"></label></div>
          <div v-if="targetForm.id" class="switch-row"><label><input v-model="targetForm.enabled" type="checkbox"> 启用此目标</label></div>
          <div class="form-actions"><button class="primary-button" :disabled="busy">{{ targetForm.id ? '保存修改' : '创建目标' }}</button><button v-if="targetForm.id" type="button" @click="editTarget()">取消</button></div>
        </form>
        <section class="admin-list"><article v-for="item in config.targets" :key="item.id" class="admin-panel entity-card"><div class="entity-title"><div><strong>{{ item.name }}</strong><code>{{ item.host }}{{ item.port ? `:${item.port}` : '' }}</code></div><span class="status-label active">{{ item.kind.toUpperCase() }}</span></div><div class="entity-meta"><span>每 {{ item.interval_seconds }} 秒</span><span>超时 {{ item.timeout_ms }}ms</span><span>{{ item.enabled ? '已启用' : '已停用' }}</span></div><div class="entity-actions"><button @click="editTarget(item)">编辑</button><button class="danger" @click="removeTarget(item)">删除</button></div></article></section>
      </template>

      <template v-else-if="tab === 'groups'">
        <section class="admin-heading"><div><span class="eyebrow">ASSIGNMENTS</span><h1>目标组</h1><p>将同类目标组成策略组，再分配给一个或多个节点。</p></div><span class="count-pill">{{ config.groups.length }} 个分组</span></section>
        <form class="admin-panel compact-form" @submit.prevent="saveGroup"><h2>{{ groupForm.id ? '编辑分组' : '添加分组' }}</h2><div class="form-grid two"><label>名称<input v-model="groupForm.name" required></label><label>类型<select v-model="groupForm.kind"><option value="ping">Ping</option><option value="tcping">TCPing</option></select></label></div><div class="form-actions"><button class="primary-button" :disabled="busy">{{ groupForm.id ? '保存修改' : '创建分组' }}</button><button v-if="groupForm.id" type="button" @click="editGroup()">取消</button></div></form>
        <section class="admin-list"><article v-for="group in config.groups" :key="group.id" class="admin-panel entity-card"><div class="entity-title"><div><strong>{{ group.name }}</strong><code>{{ group.kind.toUpperCase() }}</code></div><span class="status-label active">{{ config.group_members.filter(x => x.group_id === group.id).length }} 个目标</span></div><div class="assignment-box"><b>组内目标</b><label v-for="targetItem in config.targets.filter(x => x.kind === group.kind)" :key="targetItem.id" class="check-chip"><input type="checkbox" :checked="isGroupTarget(group.id, targetItem.id)" :disabled="busy" @change="toggleGroupTarget(group, targetItem, ($event.target as HTMLInputElement).checked)">{{ targetItem.name }}</label><span v-if="!config.targets.some(x => x.kind === group.kind)" class="empty-inline">没有兼容目标</span></div><div class="entity-actions"><button @click="editGroup(group)">编辑</button><button class="danger" @click="removeGroup(group)">删除</button></div></article></section>
      </template>

      <template v-else-if="tab === 'alerts'">
        <section class="admin-heading"><div><span class="eyebrow">NOTIFICATIONS</span><h1>通知与告警</h1><p>加密保存通知凭据，并对离线、CPU、带宽、周期流量和到期状态进行去重告警。</p></div><span class="count-pill">{{ rules.length }} 条规则</span></section>
        <div class="alert-layout">
          <section>
            <form class="admin-panel compact-form" @submit.prevent="saveChannel"><h2>{{ channelForm.id ? '编辑通知通道' : '添加通知通道' }}</h2><div class="form-grid two"><label>名称<input v-model="channelForm.name" required></label><label>类型<select v-model="channelForm.kind"><option value="webhook">Webhook</option><option value="telegram">Telegram</option></select></label><label v-if="channelForm.kind === 'webhook'">Webhook URL<input v-model="channelForm.url" type="url" :required="!channelForm.id" :placeholder="channelForm.id ? '留空则保留原凭据' : 'https://example.com/hook'"></label><template v-else><label>Bot Token<input v-model="channelForm.bot_token" type="password" :required="!channelForm.id" :placeholder="channelForm.id ? '留空则保留原凭据' : ''"></label><label>Chat ID<input v-model="channelForm.chat_id" :required="!channelForm.id"></label></template></div><div v-if="channelForm.id" class="switch-row"><label><input v-model="channelForm.enabled" type="checkbox"> 启用此通道</label></div><div class="form-actions"><button class="primary-button" :disabled="busy">{{ channelForm.id ? '保存通道' : '创建通道' }}</button><button v-if="channelForm.id" type="button" @click="editChannel()">取消</button></div></form>
            <div class="admin-list single"><article v-for="item in channels" :key="item.id" class="admin-panel entity-card"><div class="entity-title"><div><strong>{{ item.name }}</strong><code>{{ item.kind.toUpperCase() }} · 凭据已加密</code></div><span :class="['status-label', item.enabled ? 'active' : 'muted']">{{ item.enabled ? '启用' : '停用' }}</span></div><div class="entity-actions"><button @click="sendChannelTest(item)">发送测试</button><button @click="editChannel(item)">编辑</button><button class="danger" @click="removeChannel(item)">删除</button></div></article><div v-if="!channels.length" class="admin-panel empty-admin">尚未配置通知通道</div></div>
          </section>
          <section>
            <form class="admin-panel compact-form" @submit.prevent="saveRule"><h2>{{ ruleForm.id ? '编辑告警规则' : '添加告警规则' }}</h2><div class="form-grid two"><label>节点<select v-model="ruleForm.node_id" required><option value="" disabled>请选择</option><option v-for="item in nodes" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label>通知通道<select v-model="ruleForm.channel_id" required><option value="" disabled>请选择</option><option v-for="item in channels.filter(x => x.enabled)" :key="item.id" :value="item.id">{{ item.name }}</option></select></label><label>规则类型<select v-model="ruleForm.kind"><option value="offline">离线/恢复</option><option value="cpu">CPU</option><option value="bandwidth">总带宽</option><option value="cycle_traffic">周期流量</option><option value="expiry">到期</option></select></label><label>{{ thresholdLabel(ruleForm.kind) }}<input v-model.number="ruleForm.threshold" type="number" min="0" step="any" required></label><label>冷却时间（秒）<input v-model.number="ruleForm.cooldown_seconds" type="number" min="30" max="2592000" required></label></div><div v-if="ruleForm.id" class="switch-row"><label><input v-model="ruleForm.enabled" type="checkbox"> 启用此规则</label></div><div class="form-actions"><button class="primary-button" :disabled="busy || !nodes.length || !channels.length">{{ ruleForm.id ? '保存规则' : '创建规则' }}</button><button v-if="ruleForm.id" type="button" @click="editRule()">取消</button></div></form>
            <div class="admin-list single"><article v-for="item in rules" :key="item.id" class="admin-panel entity-card"><div class="entity-title"><div><strong>{{ nodeName(item.node_id) }} · {{ kindName(item.kind) }}</strong><code>{{ channelName(item.channel_id) }} · 冷却 {{ item.cooldown_seconds }} 秒</code></div><span :class="['status-label', item.enabled ? 'active' : 'muted']">{{ item.enabled ? '监控中' : '已停用' }}</span></div><div class="entity-actions"><button @click="editRule(item)">编辑</button><button class="danger" @click="removeRule(item)">删除</button></div></article><div v-if="!rules.length" class="admin-panel empty-admin">尚未创建告警规则</div></div>
          </section>
        </div>
        <section class="event-section"><h2>最近告警事件</h2><div class="event-list admin-panel"><article v-for="item in events" :key="item.id"><span :class="['event-state', item.state]">{{ item.state === 'firing' ? '告警' : item.state === 'resolved' ? '恢复' : '失败' }}</span><div><strong>{{ nodeName(item.node_id) }}</strong><p>{{ item.delivery_error || item.message }}</p></div><time>{{ new Date(item.created_at).toLocaleString('zh-CN', { hour12: false }) }}</time></article><p v-if="!events.length" class="empty-admin">暂无告警事件</p></div></section>
      </template>

      <template v-else-if="tab === 'shares'">
        <section class="admin-heading"><div><span class="eyebrow">SECURE SHARING</span><h1>图表分享</h1><p>为选定节点生成密码保护的只读历史图表链接。</p></div><span class="count-pill">{{ shares.length }} 个分享</span></section>
        <form class="admin-panel compact-form" @submit.prevent="saveShare"><h2>{{ shareForm.id ? '编辑分享' : '创建分享' }}</h2><div class="form-grid two"><label>名称<input v-model="shareForm.name" required placeholder="客户监控视图"></label><label>{{ shareForm.id ? '新密码（留空保持不变）' : '分享密码' }}<input v-model="shareForm.password" type="password" minlength="8" :required="!shareForm.id" autocomplete="new-password"></label></div><div class="assignment-box share-node-picker"><b>允许查看的节点</b><label v-for="item in nodes" :key="item.id" class="check-chip"><input v-model="shareForm.node_ids" type="checkbox" :value="item.id">{{ item.name }}</label><span v-if="!nodes.length" class="empty-inline">暂无节点</span></div><div v-if="shareForm.id" class="switch-row"><label><input v-model="shareForm.enabled" type="checkbox"> 启用此分享</label></div><div class="form-actions"><button class="primary-button" :disabled="busy || !shareForm.node_ids.length">{{ shareForm.id ? '保存分享' : '创建分享' }}</button><button v-if="shareForm.id" type="button" @click="editShare()">取消</button></div></form>
        <section class="admin-list"><article v-for="item in shares" :key="item.id" class="admin-panel entity-card"><div class="entity-title"><div><strong>{{ item.name }}</strong><code>/share/{{ item.id }}</code></div><span :class="['status-label', item.enabled ? 'active' : 'muted']">{{ item.enabled ? '可访问' : '已停用' }}</span></div><div class="entity-meta"><span>{{ item.node_ids.length }} 个节点</span><span>密码保护</span><span>只读</span></div><div class="entity-actions"><a class="entity-link" :href="`/share/${item.id}`" target="_blank">打开</a><button @click="copyShare(item)">复制链接</button><button @click="editShare(item)">编辑</button><button class="danger" @click="removeShare(item)">删除</button></div></article><div v-if="!shares.length" class="admin-panel empty-admin">尚未创建图表分享</div></section>
      </template>

      <template v-else-if="tab === 'maintenance'">
        <section class="admin-heading"><div><span class="eyebrow">PORTABILITY &amp; RECOVERY</span><h1>迁移与备份</h1><p>迁移可审阅配置，或创建包含全部数据的口令加密数据库备份。</p></div><span class="count-pill">版本 1</span></section>
        <div class="maintenance-grid">
          <section class="admin-panel maintenance-card">
            <span class="eyebrow">SAFE CONFIG</span><h2>版本化配置</h2><p>JSON 配置不包含管理员密码、Agent Token、通知密文、分享密码和历史指标。导入默认使用合并模式。</p>
            <div class="maintenance-actions"><button class="primary-button" :disabled="busy" @click="exportConfigFile">导出配置 JSON</button></div>
            <div class="maintenance-divider"></div>
            <label class="file-field">选择配置文件<input type="file" accept="application/json,.json" @change="selectConfigFile"></label>
            <div class="form-actions"><button :disabled="busy || !configFile" @click="previewConfigImport">预检导入</button><button v-if="configPreview" class="primary-button" :disabled="busy" @click="applyConfigImport">确认合并导入</button></div>
            <div v-if="configPreview" class="import-preview"><strong>预检结果</strong><span>节点：新增 {{ configPreview.nodes_created }} / 更新 {{ configPreview.nodes_updated }}</span><span>目标：新增 {{ configPreview.targets_created }} / 更新 {{ configPreview.targets_updated }}</span><span>目标组：新增 {{ configPreview.groups_created }} / 更新 {{ configPreview.groups_updated }}</span><span>新增关系：{{ configPreview.memberships_created }}</span></div>
          </section>
          <section class="admin-panel maintenance-card">
            <span class="eyebrow">FULL SNAPSHOT</span><h2>加密数据库备份</h2><p>包含认证数据、通知配置和全部历史。服务先生成 SQLite 一致快照，再使用口令分块加密。</p>
            <div class="form-grid one"><label>备份口令（至少 12 个字符）<input v-model="backupPassphrase" type="password" minlength="12" autocomplete="new-password"></label></div>
            <div class="maintenance-actions"><button class="primary-button" :disabled="busy || backupPassphrase.length < 12" @click="exportEncryptedBackup">生成并下载 .mpb</button></div>
            <div class="maintenance-divider danger-line"></div>
            <h3>恢复数据库</h3><p>上传后只做解密、完整性校验并暂存；重启时原子启用，恢复前数据库会保留为可回退副本。</p>
            <label class="file-field">选择 .mpb 备份<input type="file" accept=".mpb,application/octet-stream" @change="selectRestoreFile"></label>
            <div class="form-grid one"><label>恢复口令<input v-model="restorePassphrase" type="password" minlength="12" autocomplete="current-password"></label></div>
            <div class="maintenance-actions"><button class="danger-button" :disabled="busy || !restoreFile || restorePassphrase.length < 12" @click="stageRestore">校验并暂存恢复</button></div>
          </section>
        </div>
      </template>

      <template v-else>
        <section class="admin-heading"><div><span class="eyebrow">ACCESS &amp; ACCOUNTABILITY</span><h1>安全与审计</h1><p>修改管理员凭据，并检查所有管理操作、来源地址和结构化详情。</p></div><span class="count-pill">{{ auditEntries.length }} 条已载入</span></section>
        <div class="security-layout">
          <form class="admin-panel compact-form password-card" @submit.prevent="submitPasswordChange">
            <span class="eyebrow">PASSWORD</span><h2>修改管理员密码</h2><p>新密码至少 12 个字符。修改成功后所有管理会话都会立即撤销。</p>
            <div class="form-grid one"><label>当前密码<input v-model="currentPassword" type="password" autocomplete="current-password" required></label><label>新密码<input v-model="newPassword" type="password" minlength="12" autocomplete="new-password" required></label><label>确认新密码<input v-model="confirmPassword" type="password" minlength="12" autocomplete="new-password" required></label></div>
            <button class="primary-button" :disabled="busy || newPassword.length < 12">修改并退出所有会话</button>
          </form>
          <section class="audit-section">
            <div class="audit-list admin-panel"><article v-for="item in auditEntries" :key="item.id"><div class="audit-main"><span class="event-state firing">{{ auditLabel(item) }}</span><div><strong>{{ item.object_type }}<template v-if="item.object_id"> · {{ item.object_id }}</template></strong><p>{{ item.username || '已删除用户' }} · {{ item.remote_ip || '未知来源' }}</p></div><time>{{ new Date(item.created_at).toLocaleString('zh-CN', { hour12: false }) }}</time></div><details v-if="item.details"><summary>查看详情</summary><pre>{{ JSON.stringify(item.details, null, 2) }}</pre></details></article><p v-if="!auditEntries.length" class="empty-admin">暂无审计记录</p></div>
            <button v-if="nextAuditID" class="load-more" :disabled="busy" @click="loadMoreAudit">加载更早记录</button>
          </section>
        </div>
      </template>
    </main>

    <div v-if="nodeEdit" class="admin-overlay" @click.self="nodeEdit = null"><form class="admin-panel edit-dialog" @submit.prevent="saveNode"><header><div><span class="eyebrow">NODE SETTINGS</span><h2>{{ nodeEdit.name }}</h2></div><button type="button" class="close-button" @click="nodeEdit = null">×</button></header><div class="form-grid two"><label>名称<input v-model="nodeEdit.name" required></label><label>排序<input v-model.number="nodeEdit.sort_order" type="number"></label><label>国家/地区代码<input v-model="nodeEdit.country_code" maxlength="2"></label><label>标签（逗号分隔显示）<input :value="nodeEdit.tags.join(', ')" @input="nodeEdit!.tags = ($event.target as HTMLInputElement).value.split(',').map(x => x.trim()).filter(Boolean)"></label><label>采集间隔（秒）<input v-model.number="nodeEdit.collection_seconds" type="number" min="1" max="3600"></label><label>上报间隔（秒）<input v-model.number="nodeEdit.report_seconds" type="number" min="1" max="3600"></label><label>延迟模式<select v-model="nodeEdit.latency_mode"><option value="ping">Ping</option><option value="tcping">TCPing</option></select></label><label>流量重置日<input v-model.number="nodeEdit.traffic_reset_day" type="number" min="1" max="31" placeholder="自然月"></label><label>货币<input v-model="nodeEdit.currency" maxlength="3" placeholder="USD"></label><label>价格（最小货币单位）<input v-model.number="nodeEdit.price_minor" type="number" min="0"></label><label>计费周期<input v-model="nodeEdit.billing_cycle" placeholder="monthly"></label><label>到期时间<input v-model="nodeEdit.expires_at" type="datetime-local"></label></div><div class="switch-row"><label><input v-model="nodeEdit.hidden" type="checkbox"> 从公开面板隐藏</label><label><input v-model="nodeEdit.use_since_boot" type="checkbox"> 使用开机以来流量</label></div><div class="form-actions"><button class="primary-button" :disabled="busy">保存节点</button><button type="button" @click="nodeEdit = null">取消</button></div></form></div>

    <div v-if="customEdit" class="admin-overlay" @click.self="customEdit = null">
      <form class="admin-panel edit-dialog custom-dialog" @submit.prevent="saveCustomDisplay">
        <header><div><span class="eyebrow">CUSTOM DISPLAY</span><h2>{{ customEdit.name }}</h2></div><button type="button" class="close-button" @click="customEdit = null">×</button></header>
        <section class="custom-editor">
          <header><div><strong>自定义徽章</strong><small>最多 12 个；使用预设颜色保证亮色/暗色主题可读</small></div><button type="button" :disabled="customEdit.custom_badges!.length >= 12" @click="addCustomBadge">添加徽章</button></header>
          <div v-for="(badge, index) in customEdit.custom_badges" :key="`badge-${index}`" class="custom-editor-row badge-row"><input v-model="badge.label" maxlength="40" required placeholder="例如：CN2 GIA"><select v-model="badge.color"><option value="gray">灰色</option><option value="blue">蓝色</option><option value="green">绿色</option><option value="orange">橙色</option><option value="red">红色</option></select><button type="button" @click="customEdit.custom_badges!.splice(index, 1)">移除</button></div>
          <p v-if="!customEdit.custom_badges?.length" class="empty-inline">尚未添加徽章</p>
          <header><div><strong>自定义链接</strong><small>只允许完整的 HTTPS/HTTP 地址，最多 8 个</small></div><button type="button" :disabled="customEdit.custom_links!.length >= 8" @click="addCustomLink">添加链接</button></header>
          <div v-for="(link, index) in customEdit.custom_links" :key="`link-${index}`" class="custom-editor-row link-row"><input v-model="link.label" maxlength="60" required placeholder="名称"><input v-model="link.url" type="url" required placeholder="https://example.com"><button type="button" @click="customEdit.custom_links!.splice(index, 1)">移除</button></div>
          <p v-if="!customEdit.custom_links?.length" class="empty-inline">尚未添加链接</p>
          <label class="html-field">进阶 HTML（服务端白名单清洗）<textarea v-model="customEdit.custom_html" maxlength="16384" rows="5" placeholder="允许 p、span、strong、em、small、code、br 和安全链接；脚本、事件属性及危险协议会被移除"></textarea></label>
          <p class="security-hint">保存后展示的是服务端清洗结果，原始危险内容不会写入数据库。</p>
        </section>
        <div class="form-actions"><button class="primary-button" :disabled="busy">清洗并保存</button><button type="button" @click="customEdit = null">取消</button></div>
      </form>
    </div>

    <div v-if="token" class="admin-overlay"><section class="admin-panel token-dialog"><span class="eyebrow">ONE-TIME SECRET</span><h2>{{ tokenNode }} 的 Agent Token</h2><p>此 Token 只展示一次。复制并安全保存后再关闭。</p><code>{{ token }}</code><div class="form-actions"><button class="primary-button" @click="copyToken">复制 Token</button><button @click="token = ''">我已保存</button></div></section></div>
    <div v-if="Object.keys(importTokens).length" class="admin-overlay"><section class="admin-panel token-dialog import-token-dialog"><span class="eyebrow">ONE-TIME SECRETS</span><h2>导入节点的 Agent Token</h2><p>这些 Token 只展示一次。请全部安全保存后再关闭。</p><div class="import-token-list"><div v-for="(value, id) in importTokens" :key="id"><strong>{{ nodeName(id) }}</strong><code>{{ value }}</code></div></div><div class="form-actions"><button @click="importTokens = {}">我已全部保存</button></div></section></div>
  </div>
</template>
