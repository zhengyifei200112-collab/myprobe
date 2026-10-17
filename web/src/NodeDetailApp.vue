<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { fetchHistory, fetchSiteSettings, SessionExpiredError } from './api'
import { applyAppearance, cacheAppearance } from './appearance'
import { DsButton } from './design-system'
import { defaultSiteSettings, type HistoryResponse, type HistorySelection, type PublicNode, type ThemeMode } from './types'
import { aggregateNode, commonByteUnit, formatBytesInUnit, formatPercent, formatUptime } from './public-dashboard/metrics'
import HistoryCharts from './node-details/HistoryCharts.vue'
import { historyRanges, localDateTime, readHistorySelection, selectionQuery } from './node-details/history-view'
import './node-details/node-details.css'

const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false })
const emit = defineEmits<{ sessionExpired: [] }>()

const node = ref<PublicNode>()
const history = ref<HistoryResponse>()
const loading = ref(true)
const error = ref('')
const selection = ref<HistorySelection>('1h')
const startInput = ref('')
const endInput = ref('')
const settings = ref(defaultSiteSettings())
const theme = ref<ThemeMode>((['light', 'dark', 'system'].includes(localStorage.getItem('myprobe-theme') || '') ? localStorage.getItem('myprobe-theme') : 'system') as ThemeMode)
let controller: AbortController | undefined
let generation = 0
let poll: number | undefined
let stopped = false
const nodeID = (() => { try { return decodeURIComponent(location.pathname.match(props.admin ? /^\/admin\/nodes\/([^/]+)\/?$/ : /^\/nodes\/([^/]+)\/?$/)?.[1] || '') } catch { return '' } })()
const metrics = computed(() => node.value ? aggregateNode(node.value) : undefined)
const rateUnit = computed(() => commonByteUnit([metrics.value?.rxRate || 0, metrics.value?.txRate || 0]))
const status = computed(() => !node.value?.report ? '尚无上报' : !node.value.online ? '上报中断' : node.value.stale ? '数据陈旧' : '在线')
const dateLabel = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'

async function load(includeHistory = true) {
  const version = ++generation
  controller?.abort()
  controller = new AbortController()
  const signal = controller.signal
  if (includeHistory) { loading.value = true; history.value = undefined }
  error.value = ''
  try {
    if (!nodeID) throw new Error('节点不存在或已隐藏。')
    const response = await fetch(`/api/v1/${props.admin ? 'admin' : 'public'}/nodes/${encodeURIComponent(nodeID)}`, { cache: 'no-store', signal })
    if (props.admin && response.status === 401) throw new SessionExpiredError()
    if (!response.ok) throw new Error(response.status === 404 ? '节点不存在或已隐藏。' : '暂时无法读取节点，请重试。')
    const data = await response.json() as { node: PublicNode }
    if (version !== generation) return
    node.value = data.node
    if (includeHistory) {
      const result = await fetchHistory(nodeID, selection.value, signal, props.admin)
      if (version !== generation) return
      history.value = result
      startInput.value = localDateTime(result.start)
      endInput.value = localDateTime(result.end)
    }
  } catch (value) {
    if (signal.aborted || version !== generation) return
    node.value = undefined
    history.value = undefined
    error.value = value instanceof Error ? value.message : '加载失败，请重试。'
    if (value instanceof SessionExpiredError) emit('sessionExpired')
  } finally {
    if (version === generation) loading.value = false
  }
}
function select(value: HistorySelection) {
  selection.value = value
  window.history.pushState(null, '', `${location.pathname}?${selectionQuery(value)}`)
  void load()
}
function applyCustom() {
  try {
    const start = new Date(startInput.value).toISOString()
    const end = new Date(endInput.value).toISOString()
    select(readHistorySelection(selectionQuery({ start, end })))
  } catch { error.value = '起止时间无效，请选择不超过 365 天的范围。' }
}
function restoreURL() {
  try { selection.value = readHistorySelection(location.search); void load() }
  catch (value) { controller?.abort(); generation++; loading.value = false; node.value = undefined; history.value = undefined; error.value = (value as Error).message }
}
function changeTheme() {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  localStorage.setItem('myprobe-theme', theme.value)
  applyAppearance(settings.value, theme.value)
}
onMounted(async () => {
  window.addEventListener('popstate', restoreURL)
  restoreURL()
  poll = window.setInterval(() => { if (!loading.value && !document.hidden && node.value) void load(false) }, 30000)
  try {
    const result = await fetchSiteSettings()
    if (stopped) return
    settings.value = result
    cacheAppearance(result)
    applyAppearance(result, theme.value)
  } catch { /* Default appearance remains usable during settings failure. */ }
})
onBeforeUnmount(() => { stopped = true; generation++; controller?.abort(); clearInterval(poll); window.removeEventListener('popstate', restoreURL) })
</script>

<template>
  <div class="node-detail-page">
    <a v-if="!admin" class="skip-link" href="#main-content">跳转到节点详情</a>
    <header class="detail-header"><a :href="admin ? '/admin' : '/'" class="soft-button">← 返回节点列表</a><span>{{ admin ? '管理端节点详情' : settings.site_name }}</span><DsButton @click="changeTheme">{{ theme === 'dark' ? '浅色模式' : '深色模式' }}</DsButton></header>
    <main id="main-content" tabindex="-1">
      <header class="detail-heading"><div><span class="eyebrow">NODE DETAILS</span><h1>{{ node?.node.name || '节点详情' }}</h1><p v-if="node">{{ status }} · 最后上报 {{ dateLabel(node.node.last_seen_at) }}</p></div><DsButton :loading="loading" @click="load()">刷新数据</DsButton></header>
      <section class="detail-card detail-controls" aria-label="历史时间选择">
        <div class="detail-presets"><DsButton v-for="range in historyRanges" :key="range" :variant="selection === range ? 'primary' : 'secondary'" :aria-pressed="selection === range" @click="select(range)">{{ range }}</DsButton></div>
        <form class="detail-time-form" @submit.prevent="applyCustom"><label>开始时间<input v-model="startInput" type="datetime-local" step="1" required></label><label>结束时间<input v-model="endInput" type="datetime-local" step="1" required></label><DsButton type="submit">应用时间范围</DsButton></form>
        <p class="detail-muted">时间使用浏览器本地时区（{{ Intl.DateTimeFormat().resolvedOptions().timeZone }}），最长 365 天。结束时刻不计入结果。</p>
      </section>
      <div v-if="error" class="detail-card detail-error" role="alert"><p>{{ error }}</p><DsButton @click="restoreURL">重试</DsButton><DsButton @click="select('1h')">查看最近 1 小时</DsButton></div>
      <p v-if="loading" role="status" class="detail-empty">正在读取节点和历史数据…</p>
      <template v-if="node && !loading">
        <p v-if="admin" class="detail-muted">{{ node.node.hidden ? '此节点仅管理员可见' : '此节点已公开' }} · Agent {{ node.node.agent?.agent_version || '版本未知' }}</p>
        <section class="detail-overview" aria-label="节点概览">
          <div class="detail-card"><span>CPU</span><strong>{{ node.report ? formatPercent(node.report.cpu.usage_percent) : '—' }}</strong></div>
          <div class="detail-card"><span>内存</span><strong>{{ node.report ? formatPercent(node.report.memory.usage_percent) : '—' }}</strong></div>
          <div class="detail-card"><span>下载 / 上传</span><strong>{{ node.report ? `${formatBytesInUnit(metrics!.rxRate, rateUnit, '/s')} / ${formatBytesInUnit(metrics!.txRate, rateUnit, '/s')}` : '—' }}</strong></div>
          <div class="detail-card"><span>运行时间</span><strong>{{ node.report ? formatUptime(node.report.uptime_seconds) : '—' }}</strong></div>
        </section>
        <template v-if="history"><p class="detail-muted">{{ dateLabel(history.start) }} — {{ dateLabel(history.end) }} · 每桶 {{ history.bucket_seconds }} 秒，横轴标记桶的起点。汇总数据仅包含完整时间桶，边界可能缺少数据；空桶不连线。</p><HistoryCharts :history="history" :theme="theme" /></template>
      </template>
    </main>
  </div>
</template>
