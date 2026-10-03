<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'

interface Job { state: string; last_success_at?: string; last_completed_at?: string; failed_runs: number; completed_runs: number }
interface Health {
  observed_at: string
  server: { version?: string; uptime_seconds?: number; os: string; arch: string }
  database: { status: string; schema_version?: string; database_file: { status: string; bytes?: number }; wal_file: { status: string; bytes?: number } }
  retention: { job: Job }
  backup: { job: Job }
  scheduler: { job?: Job; last_cycle?: { assignments_loaded: boolean; due: number; dispatched: number; offline: number; failed: number; start_delay_seconds: number } }
  transport: { agent_connections: number; pending_results: number; expired_results: number }
  browser_subscriptions: number
}
const emit = defineEmits<{ unauthorized: [] }>()
const data = ref<Health | null>(null)
const busy = ref(false)
const error = ref('')
let controller: AbortController | undefined
let generation = 0
const states: Record<string, string> = { never_run: '本次启动后尚未运行', running: '运行中', success: '成功', failed: '失败', cancelled: '已取消' }
const date = (value?: string) => value ? new Date(value).toLocaleString() : '暂无记录'
const size = (value?: number) => value === undefined ? '不可用' : `${(value / 1048576).toFixed(2)} MiB`
const jobs = computed(() => data.value ? [
  { name: '历史数据保留', job: data.value.retention.job },
  { name: '探测调度', job: data.value.scheduler.job },
  { name: '加密备份文件生成', job: data.value.backup.job },
] : [])
async function refresh() {
  const current = ++generation
  controller?.abort()
  controller = new AbortController()
  busy.value = true
  error.value = ''
  try {
    const response = await fetch('/api/v1/admin/system/health', { credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
    if (current !== generation) return
    if (response.status === 401) { data.value = null; emit('unauthorized'); return }
    if (!response.ok) throw new Error('暂时无法读取系统健康，请重试。')
    const result = await response.json() as Health
    if (current === generation) data.value = result
  } catch (reason) {
    if (current !== generation) return
    error.value = reason instanceof Error && reason.name !== 'AbortError' ? reason.message : '读取已取消。'
  } finally { if (current === generation) busy.value = false }
}
onMounted(refresh)
onUnmounted(() => { generation++; controller?.abort() })
</script>

<template>
  <section class="system-health" aria-labelledby="health-title" :aria-busy="busy">
    <header class="health-heading"><div><h1 id="health-title">系统健康</h1><p>检查监控系统自身的运行状态。</p></div><button class="soft-button" :disabled="busy" @click="refresh">{{ busy ? '读取中…' : '刷新诊断' }}</button></header>
    <p v-if="error" role="alert">{{ error }} <strong v-if="data">以下为上次读取的数据，可能已经过时。</strong></p>
    <p v-else-if="busy" role="status">正在读取运行状态…</p>
    <template v-if="data">
      <p class="health-note">采样时间：{{ date(data.observed_at) }}。页面不会自动刷新；任务记录在 Server 重启后重置。</p>
      <div class="health-grid">
        <article class="admin-panel"><h2>Server</h2><dl><dt>构建版本</dt><dd>{{ data.server.version || '不可用' }}</dd><dt>运行时间</dt><dd>{{ data.server.uptime_seconds === undefined ? '不可用' : `${Math.floor(data.server.uptime_seconds / 60)} 分钟` }}</dd><dt>平台</dt><dd>{{ data.server.os }} / {{ data.server.arch }}</dd></dl></article>
        <article class="admin-panel"><h2>数据库</h2><dl><dt>元数据读取</dt><dd>{{ data.database.status === 'ok' ? '正常' : '不可用' }}</dd><dt>Schema</dt><dd>{{ data.database.schema_version || '不可用' }}</dd><dt>数据库文件</dt><dd>{{ size(data.database.database_file.bytes) }}</dd><dt>WAL 文件</dt><dd>{{ size(data.database.wal_file.bytes) }}</dd></dl></article>
        <article class="admin-panel"><h2>连接与探测</h2><dl><dt>Agent WebSocket</dt><dd>{{ data.transport.agent_connections }}</dd><dt>浏览器订阅</dt><dd>{{ data.browser_subscriptions }}</dd><dt>待返回结果</dt><dd>{{ data.transport.pending_results }}</dd><dt>已过期结果</dt><dd>{{ data.transport.expired_results }}</dd></dl><p>HTTP 上报不计入持久连接数。</p></article>
        <article v-for="item in jobs" :key="item.name" class="admin-panel"><h2>{{ item.name }}</h2><template v-if="item.job"><p>{{ states[item.job.state] || '未知状态' }}</p><dl><dt>最近完成</dt><dd>{{ date(item.job.last_completed_at) }}</dd><dt>最近成功</dt><dd>{{ date(item.job.last_success_at) }}</dd><dt>已完成 / 失败</dt><dd>{{ item.job.completed_runs }} / {{ item.job.failed_runs }}</dd></dl></template><p v-else>运行观察尚未接入。</p></article>
      </div>
      <section v-if="data.scheduler.last_cycle" class="admin-panel"><h2>最近调度周期</h2><p v-if="!data.scheduler.last_cycle.assignments_loaded">未能读取探测配置，无法确认应调度的任务数量。</p><p v-else>应执行 {{ data.scheduler.last_cycle.due }} · 已发送 {{ data.scheduler.last_cycle.dispatched }} · Agent 离线 {{ data.scheduler.last_cycle.offline }} · 发送失败 {{ data.scheduler.last_cycle.failed }}</p><p>启动延迟 {{ data.scheduler.last_cycle.start_delay_seconds.toFixed(2) }} 秒</p></section>
      <p class="health-note">备份成功仅表示加密文件已生成；下载保存和恢复能力尚未验证。持久化通知队列诊断尚未集成，不能据此判断没有积压。</p>
    </template>
  </section>
</template>

<style scoped>
.system-health { display: grid; gap: 18px; min-width: 0; }
.health-heading { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 12px; }
.health-heading h1 { margin: 0; }.health-heading p,.health-note { color: var(--muted); line-height: 1.7; }
.health-grid { display: grid; grid-template-columns: repeat(auto-fit,minmax(min(100%,280px),1fr)); gap: 16px; }
.system-health article,.system-health section.admin-panel { padding: 20px; min-width: 0; }
.system-health h2 { font-size: 17px; margin: 0 0 16px; }
.system-health dl { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; font-size: 14px; }
.system-health dt { color: var(--muted); }.system-health dd { margin: 0; text-align: right; overflow-wrap: anywhere; }
.system-health p { overflow-wrap: anywhere; }
</style>
