<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { HistoryRange, HistoryResponse, PublicNode } from './types'

type Theme = 'light' | 'dark'
const shareID = decodeURIComponent(location.pathname.split('/').filter(Boolean)[1] || '')
const name = ref('共享图表')
const authenticated = ref(false)
const available = ref(true)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const password = ref('')
const nodes = ref<PublicNode[]>([])
const selectedID = ref('')
const range = ref<HistoryRange>('1h')
const ranges: HistoryRange[] = ['1h', '12h', '1d', '3d', '7d', '30d', '1y']
const resourceElement = ref<HTMLElement>()
const latencyElement = ref<HTMLElement>()
const trafficElement = ref<HTMLElement>()
const theme = ref<Theme>((localStorage.getItem('myprobe-theme') as Theme | null) ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))
let resourceChart: any
let latencyChart: any
let trafficChart: any

function api(path: string) { return `/api/v1/share/${encodeURIComponent(shareID)}${path}` }

async function jsonRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(api(path), { ...options, credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json', ...(options.body ? { 'Content-Type': 'application/json' } : {}) } })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({})) as { error?: string }
    if (response.status === 401) authenticated.value = false
    if (response.status === 429) throw new Error('密码尝试过多，请稍后再试。')
    throw new Error(payload.error || `请求失败（${response.status}）`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

async function loadMeta() {
  let meta: { name: string; authenticated: boolean }
  try {
    meta = await jsonRequest<{ name: string; authenticated: boolean }>('/meta')
  } catch (value) {
    available.value = false
    error.value = value instanceof Error ? value.message : '分享链接不可用'
    loading.value = false
    return
  }
  name.value = meta.name
  authenticated.value = meta.authenticated
  if (meta.authenticated) {
    try {
      await loadNodes()
    } catch (value) {
      error.value = value instanceof Error ? value.message : '分享会话已失效'
    }
  }
  loading.value = false
}

async function login() {
  busy.value = true
  error.value = ''
  try {
    await jsonRequest('/login', { method: 'POST', body: JSON.stringify({ password: password.value }) })
    password.value = ''
    authenticated.value = true
    await loadNodes()
  } catch (value) {
    error.value = value instanceof Error ? value.message : '登录失败'
  } finally {
    busy.value = false
  }
}

async function logout() {
  await jsonRequest('/logout', { method: 'POST' }).catch(() => undefined)
  authenticated.value = false
  nodes.value = []
  disposeCharts()
}

async function loadNodes() {
  const result = await jsonRequest<{ nodes: PublicNode[] }>('/nodes')
  nodes.value = result.nodes
  if (!nodes.value.some(item => item.node.id === selectedID.value)) selectedID.value = nodes.value[0]?.node.id || ''
  await nextTick()
  if (selectedID.value) await loadHistory()
}

async function loadHistory() {
  if (!selectedID.value) return
  busy.value = true
  error.value = ''
  try {
    const history = await jsonRequest<HistoryResponse>(`/nodes/${encodeURIComponent(selectedID.value)}/history?range=${range.value}`)
    await nextTick()
    await renderCharts(history)
  } catch (value) {
    if (!authenticated.value) disposeCharts()
    error.value = value instanceof Error ? value.message : '读取历史数据失败'
  } finally {
    busy.value = false
  }
}

async function renderCharts(history: HistoryResponse) {
  if (!resourceElement.value || !latencyElement.value || !trafficElement.value) return
  const { default: echarts } = await import('./charting')
  disposeCharts()
  resourceChart = echarts.init(resourceElement.value)
  latencyChart = echarts.init(latencyElement.value)
  trafficChart = echarts.init(trafficElement.value)
  const styles = getComputedStyle(document.documentElement)
  const color = (name: string) => styles.getPropertyValue(name).trim()
  const text = color('--muted'), border = color('--border'), blue = color('--blue'), cyan = color('--cyan'), green = color('--green'), orange = color('--orange'), purple = color('--purple')
  const common = { animationDuration: 300, textStyle: { color: text, fontFamily: 'inherit' }, tooltip: { trigger: 'axis', backgroundColor: color('--surface-strong'), borderColor: border, textStyle: { color: color('--text') } }, legend: { top: 0, textStyle: { color: text } }, grid: { left: 48, right: 52, top: 38, bottom: 28 }, xAxis: { type: 'time', splitNumber: 4, axisLine: { lineStyle: { color: border } }, axisLabel: { color: text, fontSize: 9, hideOverlap: true } } }
  resourceChart.setOption({ ...common, yAxis: [{ type: 'value', min: 0, max: 100, axisLabel: { color: text, formatter: '{value}%' }, splitLine: { lineStyle: { color: border } } }, { type: 'value', min: 0, axisLabel: { color: text, formatter: (value: number) => formatBytes(value, '/s') }, splitLine: { show: false } }], series: [
    { name: 'CPU', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map(p => [p.time, p.cpu_percent]), lineStyle: { color: blue } },
    { name: '内存', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map(p => [p.time, p.memory_percent]), lineStyle: { color: cyan } },
    { name: '硬盘', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map(p => [p.time, p.disk_percent]), lineStyle: { color: purple } },
    { name: '上传', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map(p => [p.time, p.tx_bytes_per_second]), lineStyle: { color: orange } },
    { name: '下载', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map(p => [p.time, p.rx_bytes_per_second]), lineStyle: { color: green } },
  ] })
  const targets = new Map<string, { name: string; points: Array<[string, number | null]> }>()
  for (const point of history.latency) { const item = targets.get(point.target_id) ?? { name: `${point.kind === 'tcping' ? 'TCP' : 'Ping'} · ${point.name}`, points: [] }; item.points.push([point.time, point.latency_ms ?? null]); targets.set(point.target_id, item) }
  latencyChart.setOption({ ...common, yAxis: { type: 'value', min: 0, axisLabel: { color: text, formatter: '{value} ms' }, splitLine: { lineStyle: { color: border } } }, series: [...targets.values()].map(item => ({ name: item.name, type: 'line', connectNulls: false, showSymbol: false, smooth: true, data: item.points })) })
  trafficChart.setOption({ ...common, yAxis: { type: 'value', min: 0, axisLabel: { color: text, formatter: (value: number) => formatBytes(value) }, splitLine: { lineStyle: { color: border } } }, series: [
    { name: '上传累计', type: 'line', showSymbol: false, data: history.traffic.map(p => [p.time, p.tx_bytes]), lineStyle: { color: orange } },
    { name: '下载累计', type: 'line', showSymbol: false, data: history.traffic.map(p => [p.time, p.rx_bytes]), lineStyle: { color: green } },
    { name: '总流量', type: 'line', showSymbol: false, data: history.traffic.map(p => [p.time, p.total_bytes]), lineStyle: { color: blue } },
  ] })
}

function formatBytes(value = 0, suffix = '') { const units = ['B', 'KB', 'MB', 'GB', 'TB']; let size = Math.max(0, value), index = 0; while (size >= 1024 && index < units.length - 1) { size /= 1024; index++ } return `${size.toFixed(index === 0 ? 0 : size >= 100 ? 0 : 1)} ${units[index]}${suffix}` }
function disposeCharts() { resourceChart?.dispose(); latencyChart?.dispose(); trafficChart?.dispose(); resourceChart = latencyChart = trafficChart = undefined }
function toggleTheme() { theme.value = theme.value === 'light' ? 'dark' : 'light'; document.documentElement.dataset.theme = theme.value; localStorage.setItem('myprobe-theme', theme.value); void nextTick().then(loadHistory) }
function resize() { resourceChart?.resize(); latencyChart?.resize(); trafficChart?.resize() }

onMounted(() => { document.documentElement.dataset.theme = theme.value; window.addEventListener('resize', resize); void loadMeta() })
onBeforeUnmount(() => { window.removeEventListener('resize', resize); disposeCharts() })
</script>

<template>
  <div class="share-shell">
    <header class="admin-nav"><a class="brand" href="/"><span class="brand-mark">MP</span><span>MyProbe <small>安全共享</small></span></a><div class="nav-actions"><button class="soft-button" @click="toggleTheme">{{ theme === 'light' ? '深色' : '浅色' }}</button><button v-if="authenticated" class="soft-button" @click="logout">退出</button></div></header>
    <main v-if="loading" class="state-panel"><div class="loader"></div><p>正在验证分享链接…</p></main>
    <main v-else-if="!available" class="state-panel"><div class="empty-icon">×</div><p>{{ error || '分享链接不存在或已停用。' }}</p></main>
    <main v-else-if="!authenticated" class="login-wrap"><form class="admin-panel login-card" @submit.prevent="login"><span class="eyebrow">PROTECTED CHARTS</span><h1>{{ name }}</h1><p>此图表由密码保护，请输入分享密码继续。</p><label>分享密码<input v-model="password" type="password" autocomplete="current-password" required autofocus></label><p v-if="error" class="form-message error">{{ error }}</p><button class="primary-button" :disabled="busy">{{ busy ? '验证中…' : '查看图表' }}</button></form></main>
    <main v-else class="share-main"><section class="admin-heading"><div><span class="eyebrow">READ-ONLY MONITORING</span><h1>{{ name }}</h1><p>仅展示分享范围内的节点历史数据。</p></div><span class="count-pill">{{ nodes.length }} 个节点</span></section><div v-if="error" class="admin-alert error">{{ error }}</div><div v-if="!nodes.length" class="admin-panel empty-admin">分享范围内暂无可用节点</div><template v-else><nav class="share-controls admin-panel"><label>节点<select v-model="selectedID" @change="loadHistory"><option v-for="item in nodes" :key="item.node.id" :value="item.node.id">{{ item.node.name }}</option></select></label><div class="range-switch"><button v-for="item in ranges" :key="item" :class="{ active: range === item }" :disabled="busy" @click="range = item; loadHistory()">{{ item }}</button></div></nav><section class="share-chart-grid"><article class="chart-block"><h3>资源与实时速率</h3><div ref="resourceElement" class="chart-canvas"></div></article><article class="chart-block"><h3>Ping / TCPing 延迟</h3><div ref="latencyElement" class="chart-canvas"></div></article><article class="chart-block full"><h3>上传 / 下载 / 总流量累计</h3><div ref="trafficElement" class="chart-canvas"></div></article></section></template></main>
  </div>
</template>
