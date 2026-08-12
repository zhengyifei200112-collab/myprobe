<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { connectRealtime, fetchHistory, fetchNodes } from './api'
import type { HistoryRange, HistoryResponse, PublicNode, RealtimeEvent } from './types'

type Theme = 'light' | 'dark'
type DisplayMode = 'compact' | 'detailed'

const nodes = ref<PublicNode[]>([])
const activeTag = ref('__all__')
const loading = ref(true)
const error = ref('')
const connected = ref(false)
const now = ref(new Date())
const chartNode = ref<PublicNode>()
const chartRange = ref<HistoryRange>('1h')
const chartLoading = ref(false)
const chartError = ref('')
const resourceChartElement = ref<HTMLElement>()
const latencyChartElement = ref<HTMLElement>()
const trafficChartElement = ref<HTMLElement>()
const historyRanges: HistoryRange[] = ['1h', '12h', '1d', '3d', '7d', '30d', '1y']
const initialTheme = localStorage.getItem('myprobe-theme') as Theme | null
const theme = ref<Theme>(initialTheme ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))
const initialDisplayMode = localStorage.getItem('myprobe-display-mode') as DisplayMode | null
const displayMode = ref<DisplayMode>(initialDisplayMode === 'detailed' ? 'detailed' : 'compact')
let disconnect: (() => void) | undefined
let clock: number | undefined
let resourceChart: any
let latencyChart: any
let trafficChart: any

const sortedNodes = computed(() => [...nodes.value].sort((a, b) => a.node.sort_order - b.node.sort_order || a.node.name.localeCompare(b.node.name)))
const tags = computed(() => {
  const counts = new Map<string, number>()
  for (const item of nodes.value) for (const tag of item.node.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1)
  return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b, 'zh-CN', { numeric: true }))
})
const visibleNodes = computed(() => activeTag.value === '__all__'
  ? sortedNodes.value
  : sortedNodes.value.filter((item) => item.node.tags?.includes(activeTag.value)))
const onlineCount = computed(() => visibleNodes.value.filter((item) => item.online).length)
const totalRate = computed(() => sumNetwork(visibleNodes.value, 'rate'))
const totalTraffic = computed(() => sumNetwork(visibleNodes.value, 'total'))

function mergeEvent(event: RealtimeEvent) {
  if (event.type === 'snapshot') {
    nodes.value = event.nodes
    return
  }
  if (event.type === 'node_metrics') {
    const index = nodes.value.findIndex((item) => item.node.id === event.node.node.id)
    if (index === -1) nodes.value.push(event.node)
    else nodes.value[index] = event.node
  }
}

async function load() {
  try {
    const response = await fetchNodes()
    nodes.value = response.nodes
    localStorage.setItem('myprobe-nodes', JSON.stringify(nodes.value))
    error.value = ''
  } catch {
    error.value = '暂时无法获取最新数据'
    const cached = localStorage.getItem('myprobe-nodes')
    if (cached) {
      try { nodes.value = JSON.parse(cached) as PublicNode[] } catch { /* ignored */ }
    }
  } finally {
    loading.value = false
  }
}

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
  document.documentElement.dataset.theme = theme.value
  localStorage.setItem('myprobe-theme', theme.value)
  if (chartNode.value) void loadHistory()
}

function toggleDisplayMode() {
  displayMode.value = displayMode.value === 'compact' ? 'detailed' : 'compact'
  localStorage.setItem('myprobe-display-mode', displayMode.value)
}

function aggregate(item: PublicNode) {
  const report = item.report
  const disks = report?.disks ?? []
  const networks = report?.networks ?? []
  const diskTotal = disks.reduce((sum, disk) => sum + disk.total_bytes, 0)
  const diskUsed = disks.reduce((sum, disk) => sum + disk.used_bytes, 0)
  return {
    diskTotal,
    diskPercent: diskTotal ? diskUsed / diskTotal * 100 : 0,
    rxRate: networks.reduce((sum, network) => sum + network.rx_bytes_per_second, 0),
    txRate: networks.reduce((sum, network) => sum + network.tx_bytes_per_second, 0),
    rxTotal: networks.reduce((sum, network) => sum + network.rx_total_bytes, 0),
    txTotal: networks.reduce((sum, network) => sum + network.tx_total_bytes, 0),
  }
}

function sumNetwork(items: PublicNode[], kind: 'rate' | 'total') {
  let up = 0
  let down = 0
  for (const item of items) {
    const metrics = aggregate(item)
    up += kind === 'rate' ? metrics.txRate : metrics.txTotal
    down += kind === 'rate' ? metrics.rxRate : metrics.rxTotal
  }
  return { up, down }
}

function formatBytes(value: number, suffix = '') {
  if (!Number.isFinite(value) || value <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const scaled = value / 1024 ** index
  return `${scaled >= 100 ? scaled.toFixed(0) : scaled >= 10 ? scaled.toFixed(1) : scaled.toFixed(2)} ${units[index]}${suffix}`
}

function formatUptime(seconds = 0) {
  if (!seconds) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  return days ? `${days}天${hours}时` : hours ? `${hours}时${minutes}分` : `${minutes}分钟`
}

function countryCode(code: string) {
  const normalized = code?.trim().toLowerCase()
  return /^[a-z]{2}$/.test(normalized) ? normalized : ''
}

function maskedIP(value?: string) {
  if (!value) return '—'
  const parts = value.split('.')
  return parts.length === 4 ? `${parts[0]}.${parts[1]}.**` : value.replace(/:[^:]+$/, ':****')
}

function percent(value = 0) {
  return `${Math.max(0, Math.min(100, value)).toFixed(value >= 10 ? 0 : 1)}%`
}

function barClass(value = 0) {
  return value >= 90 ? 'danger' : value >= 75 ? 'warning' : ''
}

function price(item: PublicNode) {
  if (item.node.price_minor == null || !item.node.currency) return '未设置价格'
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: item.node.currency,
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
    useGrouping: false,
  }).format(item.node.price_minor / 100)
}

function expiry(item: PublicNode) {
  if (!item.node.expires_at) return '无到期时间'
  if (!item.commercial) return '到期状态未知'
  return item.commercial.expired ? `已过期${item.commercial.days}天` : `剩${item.commercial.days}天`
}

function expiryDate(item: PublicNode) {
  if (!item.node.expires_at) return ''
  const parsed = new Date(item.node.expires_at)
  if (Number.isNaN(parsed.getTime())) return ''
  return `${parsed.getFullYear()}/${String(parsed.getMonth() + 1).padStart(2, '0')}/${String(parsed.getDate()).padStart(2, '0')}到期`
}

function osName(item: PublicNode) {
  const value = item.node.agent?.platform || item.node.agent?.operating_system || ''
  if (!value) return '等待 Agent 上报'
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function latencyText(success?: boolean, latency?: number, errorClass?: string) {
  if (success === undefined) return '等待首次探测'
  if (!success) return errorClass ? `失败 · ${errorClass}` : '探测失败'
  if (latency === undefined) return '已连通'
  return `${latency < 10 ? latency.toFixed(2) : latency.toFixed(1)} ms`
}

async function openHistory(item: PublicNode) {
  chartNode.value = item
  chartRange.value = '1h'
  await nextTick()
  await loadHistory()
}

async function loadHistory() {
  if (!chartNode.value) return
  chartLoading.value = true
  chartError.value = ''
  try {
    const history = await fetchHistory(chartNode.value.node.id, chartRange.value)
    await nextTick()
    await renderHistory(history)
  } catch {
    chartError.value = '暂时无法读取历史数据'
  } finally {
    chartLoading.value = false
  }
}

async function renderHistory(history: HistoryResponse) {
  if (!resourceChartElement.value || !latencyChartElement.value || !trafficChartElement.value) return
  const { default: echarts } = await import('./charting')
  resourceChart?.dispose()
  latencyChart?.dispose()
  trafficChart?.dispose()
  resourceChart = echarts.init(resourceChartElement.value)
  latencyChart = echarts.init(latencyChartElement.value)
  trafficChart = echarts.init(trafficChartElement.value)
  const styles = getComputedStyle(document.documentElement)
  const text = styles.getPropertyValue('--muted').trim()
  const border = styles.getPropertyValue('--border').trim()
  const blue = styles.getPropertyValue('--blue').trim()
  const cyan = styles.getPropertyValue('--cyan').trim()
  const green = styles.getPropertyValue('--green').trim()
  const orange = styles.getPropertyValue('--orange').trim()
  const purple = styles.getPropertyValue('--purple').trim()
  const common = {
    animationDuration: 300,
    textStyle: { color: text, fontFamily: 'inherit' },
    tooltip: { trigger: 'axis', backgroundColor: styles.getPropertyValue('--surface-strong').trim(), borderColor: border, textStyle: { color: styles.getPropertyValue('--text').trim() } },
    legend: { top: 0, textStyle: { color: text } },
    grid: { left: 44, right: 48, top: 38, bottom: 28 },
    xAxis: { type: 'time', splitNumber: 4, axisLine: { lineStyle: { color: border } }, axisLabel: { color: text, fontSize: 9, hideOverlap: true } },
  }
  resourceChart.setOption({
    ...common,
    yAxis: [
      { type: 'value', min: 0, max: 100, axisLabel: { color: text, formatter: '{value}%' }, splitLine: { lineStyle: { color: border } } },
      { type: 'value', min: 0, axisLabel: { color: text, formatter: (value: number) => formatBytes(value, '/s') }, splitLine: { show: false } },
    ],
    series: [
      { name: 'CPU', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.cpu_percent]), lineStyle: { color: blue }, itemStyle: { color: blue } },
      { name: '内存', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.memory_percent]), lineStyle: { color: cyan }, itemStyle: { color: cyan } },
      { name: '硬盘', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.disk_percent]), lineStyle: { color: purple }, itemStyle: { color: purple } },
      { name: '上传', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map((p) => [p.time, p.tx_bytes_per_second]), lineStyle: { color: orange }, itemStyle: { color: orange } },
      { name: '下载', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map((p) => [p.time, p.rx_bytes_per_second]), lineStyle: { color: green }, itemStyle: { color: green } },
    ],
  })
  const targets = new Map<string, { name: string; points: Array<[string, number | null]> }>()
  for (const point of history.latency) {
    const target = targets.get(point.target_id) ?? { name: `${point.kind === 'tcping' ? 'TCP' : 'Ping'} · ${point.name}`, points: [] }
    target.points.push([point.time, point.latency_ms ?? null])
    targets.set(point.target_id, target)
  }
  latencyChart.setOption({
    ...common,
    yAxis: { type: 'value', min: 0, axisLabel: { color: text, formatter: '{value} ms' }, splitLine: { lineStyle: { color: border } } },
    series: [...targets.values()].map((target) => ({ name: target.name, type: 'line', connectNulls: false, showSymbol: false, smooth: true, data: target.points })),
  })
  trafficChart.setOption({
    ...common,
    yAxis: { type: 'value', min: 0, axisLabel: { color: text, formatter: (value: number) => formatBytes(value) }, splitLine: { lineStyle: { color: border } } },
    series: [
      { name: '上传累计', type: 'line', showSymbol: false, data: history.traffic.map((p) => [p.time, p.tx_bytes]), lineStyle: { color: orange }, itemStyle: { color: orange } },
      { name: '下载累计', type: 'line', showSymbol: false, data: history.traffic.map((p) => [p.time, p.rx_bytes]), lineStyle: { color: green }, itemStyle: { color: green } },
      { name: '总流量', type: 'line', showSymbol: false, data: history.traffic.map((p) => [p.time, p.total_bytes]), lineStyle: { color: blue }, itemStyle: { color: blue } },
    ],
  })
}

function closeHistory() {
  chartNode.value = undefined
  resourceChart?.dispose()
  latencyChart?.dispose()
  trafficChart?.dispose()
  resourceChart = undefined
  latencyChart = undefined
  trafficChart = undefined
}

function resizeCharts() {
  resourceChart?.resize()
  latencyChart?.resize()
  trafficChart?.resize()
}

onMounted(() => {
  document.documentElement.dataset.theme = theme.value
  void load()
  disconnect = connectRealtime((event) => {
    mergeEvent(event)
    localStorage.setItem('myprobe-nodes', JSON.stringify(nodes.value))
  }, (state) => { connected.value = state })
  clock = window.setInterval(() => { now.value = new Date() }, 1000)
  window.addEventListener('resize', resizeCharts)
})

onBeforeUnmount(() => {
  disconnect?.()
  if (clock !== undefined) window.clearInterval(clock)
  window.removeEventListener('resize', resizeCharts)
  resourceChart?.dispose()
  latencyChart?.dispose()
  trafficChart?.dispose()
})
</script>

<template>
  <div class="app-shell">
    <header class="navbar">
      <div class="navbar-inner">
        <a class="brand" href="/" aria-label="MyProbe 首页">
          <span class="brand-mark">MP</span>
          <span class="brand-copy">
            <span class="brand-title">MyProbe</span>
            <span class="brand-subtitle">Server Monitor</span>
          </span>
        </a>
        <div class="nav-actions">
          <button
            class="soft-button mode-button"
            type="button"
            :title="displayMode === 'compact' ? '切换到详情显示模式' : '切换到简洁显示模式'"
            :aria-label="displayMode === 'compact' ? '切换到详情显示模式' : '切换到简洁显示模式'"
            @click="toggleDisplayMode"
          >
            <span class="button-icon" aria-hidden="true">
              <svg v-if="displayMode === 'compact'" viewBox="0 0 16 16"><path d="M6 2H2v4M10 2h4v4M14 10v4h-4M6 14H2v-4" /></svg>
              <svg v-else viewBox="0 0 16 16"><path d="M2 6h4V2M14 6h-4V2M10 14v-4h4M6 14v-4H2" /></svg>
            </span>
            {{ displayMode === 'compact' ? '详情' : '简洁' }}
          </button>
          <button class="soft-button" type="button" :aria-label="theme === 'light' ? '切换到暗色主题' : '切换到亮色主题'" @click="toggleTheme">
            <span class="button-icon" aria-hidden="true">
              <svg v-if="theme === 'light'" viewBox="0 0 16 16"><circle cx="8" cy="8" r="2.6" /><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.05 3.05l1.4 1.4M11.55 11.55l1.4 1.4M12.95 3.05l-1.4 1.4M4.45 11.55l-1.4 1.4" /></svg>
              <svg v-else viewBox="0 0 16 16"><path d="M13.5 10.6A6 6 0 0 1 5.4 2.5 6 6 0 1 0 13.5 10.6Z" /></svg>
            </span>
            {{ theme === 'light' ? '暗色' : '亮色' }}
          </button>
          <a class="soft-button admin-link" href="/admin"><span class="button-icon" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="M9 3h4v10H9M7 5l3 3-3 3M10 8H2" /></svg></span>后台</a>
        </div>
      </div>
    </header>

    <main>
      <section class="dashboard-intro" aria-labelledby="dashboard-title">
        <div>
          <div class="dashboard-eyebrow">Infrastructure overview</div>
          <h1 id="dashboard-title">服务器运行概览</h1>
          <p>节点状态、资源占用、实时速率与网络延迟集中展示，数据自动刷新。</p>
        </div>
        <div class="live-badge" :class="{ reconnecting: !connected }" :title="connected ? 'WebSocket 实时连接正常' : '正在重新连接实时数据'">
          <span class="live-dot" aria-hidden="true"></span>
          {{ connected ? '实时监控中' : '正在重连' }}
        </div>
      </section>

      <section class="overview-grid" aria-label="总览">
        <article class="overview-card overview-time-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5l3.5 2"/></svg></span><span class="overview-title">当前时间</span></div>
          <div class="overview-content">
            <strong class="overview-value clock">{{ now.toLocaleTimeString('zh-CN', { hour12: false }) }}</strong>
            <small>{{ now.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }) }}</small>
          </div>
        </article>
        <article class="overview-card overview-status-card" :title="`当前筛选：总数 ${visibleNodes.length} • 在线 ${onlineCount} • 离线 ${visibleNodes.length - onlineCount}`">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="4.5" width="16" height="6" rx="2"/><rect x="4" y="13.5" width="16" height="6" rx="2"/><path d="M8 7.5h.01M8 16.5h.01M12 7.5h5M12 16.5h5"/></svg></span><span class="overview-title">服务器概况</span></div>
          <div class="overview-content">
            <div class="overview-main-row"><strong class="overview-main-number">{{ visibleNodes.length }}</strong><span>台节点</span></div>
            <div class="status-breakdown"><span><b class="dot online"></b>在线 <strong>{{ onlineCount }}</strong></span><span><b class="dot offline"></b>离线 <strong>{{ visibleNodes.length - onlineCount }}</strong></span></div>
          </div>
        </article>
        <article class="overview-card overview-traffic-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 19V9M10 19V5M15 19v-7M20 19V7"/><path d="M3.5 19.5h18"/></svg></span><span class="overview-title">累计流量</span></div>
          <div class="overview-content overview-pairline">
            <span><small><i class="up-arrow">↑</i> 上传</small><strong class="overview-value">{{ formatBytes(totalTraffic.up) }}</strong></span>
            <span><small><i class="down-arrow">↓</i> 下载</small><strong class="overview-value">{{ formatBytes(totalTraffic.down) }}</strong></span>
          </div>
        </article>
        <article class="overview-card overview-speed-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 8.5h4l2.2-4 4.2 15 2.5-8h4.1"/></svg></span><span class="overview-title">实时速率</span></div>
          <div class="overview-content overview-pairline">
            <span><small><i class="up-arrow">↑</i> 上传</small><strong class="overview-value">{{ formatBytes(totalRate.up, '/s') }}</strong></span>
            <span><small><i class="down-arrow">↓</i> 下载</small><strong class="overview-value">{{ formatBytes(totalRate.down, '/s') }}</strong></span>
          </div>
        </article>
      </section>

      <section class="nodes-section" aria-labelledby="nodes-title">
        <div class="nodes-toolbar">
          <div class="section-heading">
            <div><span class="section-kicker">Infrastructure</span><h2 id="nodes-title">节点列表</h2></div>
            <div class="section-counter"><strong>{{ visibleNodes.length }}</strong> 个节点</div>
          </div>
          <div class="filter-section">
            <div class="filter-bar" aria-label="标签筛选">
              <button :class="{ active: activeTag === '__all__' }" @click="activeTag = '__all__'">全部 <span>{{ nodes.length }}</span></button>
              <button v-for="([tag, count]) in tags" :key="tag" :class="{ active: activeTag === tag }" @click="activeTag = tag">
                {{ tag }} <span>{{ count }}</span>
              </button>
            </div>
          </div>
        </div>
      </section>

      <div v-if="error" class="notice">{{ error }}，当前展示最后缓存数据。</div>
      <div v-if="loading" class="state-panel">
        <span class="loader"></span><strong>正在读取节点…</strong>
      </div>
      <div v-else-if="visibleNodes.length === 0" class="state-panel empty">
        <div class="empty-icon">◇</div>
        <strong>还没有可显示的节点</strong>
        <p>在管理后台注册第一台服务器后，数据会实时出现在这里。</p>
      </div>

      <section v-else class="node-grid" :class="displayMode" aria-live="polite">
        <article
          v-for="item in visibleNodes"
          :key="item.node.id"
          class="node-card"
          :class="{ offline: !item.online, stale: item.stale }"
          role="button"
          tabindex="0"
          :aria-label="`${item.node.name} 详情卡片`"
          @click="openHistory(item)"
          @keydown.enter="openHistory(item)"
          @keydown.space.prevent="openHistory(item)"
        >
          <header class="node-header">
            <div class="node-title">
              <span
                class="flag"
                role="img"
                :aria-label="countryCode(item.node.country_code) ? `${item.node.country_code.toUpperCase()} 国旗` : '未设置国家或地区'"
              >
                <span
                  v-if="countryCode(item.node.country_code)"
                  class="country-flag"
                  :class="`flag:${countryCode(item.node.country_code).toUpperCase()}`"
                  aria-hidden="true"
                ></span>
                <span v-else aria-hidden="true">🌐</span>
              </span>
              <strong>{{ item.node.name }}</strong>
            </div>
            <span class="node-status" :class="{ online: item.online }" role="status"><i></i>{{ item.online ? '在线' : '离线' }}</span>
          </header>

          <div class="node-badges">
            <span class="price-badge">{{ price(item) }}<template v-if="item.node.billing_cycle">/{{ item.node.billing_cycle }}</template></span>
            <span :class="{ overdue: item.commercial?.expired }">{{ expiry(item) }}</span>
            <span v-if="expiryDate(item)" class="expiry-date">{{ expiryDate(item) }}</span>
          </div>

          <div class="quick-facts" aria-label="节点基础信息">
            <div><span class="quick-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M3.8 12h16.4M12 3.5c2.4 2.3 3.6 5.1 3.6 8.5S14.4 18.2 12 20.5M12 3.5C9.6 5.8 8.4 8.6 8.4 12s1.2 6.2 3.6 8.5"/></svg></span><span><small>公网 IP</small><strong>{{ maskedIP(item.report?.public_ip) }}</strong></span></div>
            <div><span class="quick-icon clock-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5l3.5 2"/></svg></span><span><small>在线时长</small><strong>{{ formatUptime(item.report?.uptime_seconds) }}</strong></span></div>
          </div>

          <div class="detail-content">
            <section class="network-panel" aria-label="网络流量">
              <header><strong><span class="section-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 8.5h4l2.2-4 4.2 15 2.5-8h4.1"/></svg></span>网络流量</strong><small>{{ item.node.traffic_reset_day ? `每月${item.node.traffic_reset_day}日重置` : '自然月重置' }}</small></header>
              <div class="network-grid">
                <div><small>实时速率</small><span><b class="up-arrow">↑</b><strong>{{ formatBytes(aggregate(item).txRate, '/s') }}</strong></span><span><b class="down-arrow">↓</b><strong>{{ formatBytes(aggregate(item).rxRate, '/s') }}</strong></span></div>
                <div><small>{{ item.node.use_since_boot ? '开机累计' : '累计流量' }}</small><span><b class="up-arrow">↑</b><strong>{{ formatBytes(aggregate(item).txTotal) }}</strong></span><span><b class="down-arrow">↓</b><strong>{{ formatBytes(aggregate(item).rxTotal) }}</strong></span></div>
                <div><small>本周期</small><span><b class="up-arrow">↑</b><strong>{{ formatBytes(item.traffic?.tx_bytes || 0) }}</strong></span><span><b class="down-arrow">↓</b><strong>{{ formatBytes(item.traffic?.rx_bytes || 0) }}</strong></span></div>
              </div>
            </section>

            <section class="resource-panel" aria-label="资源使用">
              <header>
                <strong><span class="section-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7v5l3 2M5.8 17.8l2.1-2.1"/></svg></span>资源使用</strong>
                <div class="hardware-line"><span><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="2"/><path d="M9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4"/></svg>{{ item.report?.cpu.logical_cores || '—' }}C</span><span><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="7" width="16" height="10" rx="2"/><path d="M8 10v4M12 10v4M16 10v4M7 4v3M17 4v3M7 17v3M17 17v3"/></svg>{{ formatBytes(item.report?.memory.total_bytes || 0) }}</span><span><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="5" width="16" height="14" rx="2"/><path d="M4 14h16M8 17h.01"/></svg>{{ formatBytes(aggregate(item).diskTotal) }}</span></div>
              </header>
              <div class="resource-grid">
                <div><span><b>CPU</b><strong>{{ percent(item.report?.cpu.usage_percent) }}</strong></span><div class="bar"><i :class="barClass(item.report?.cpu.usage_percent)" :style="{ width: percent(item.report?.cpu.usage_percent) }"></i></div></div>
                <div><span><b>内存</b><strong>{{ percent(item.report?.memory.usage_percent) }}</strong></span><div class="bar"><i :class="barClass(item.report?.memory.usage_percent)" :style="{ width: percent(item.report?.memory.usage_percent) }"></i></div></div>
                <div><span><b>硬盘</b><strong>{{ percent(aggregate(item).diskPercent) }}</strong></span><div class="bar"><i :class="barClass(aggregate(item).diskPercent)" :style="{ width: percent(aggregate(item).diskPercent) }"></i></div></div>
              </div>
            </section>

            <section class="latency-panel" :class="{ empty: !item.latency?.length }" aria-label="网络延迟">
              <header><strong><span class="section-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 18v-3M8 18v-6M12 18V9M16 18V6M20 18V3"/></svg></span>网络延迟</strong><small>最近探测</small></header>
              <div class="latency-grid" :style="{ '--latency-columns': Math.min(item.latency?.length || 1, 3) }">
                <div v-for="latency in item.latency?.slice(0, 3)" :key="latency.target_id" class="latency-item">
                  <small><i :class="{ failed: latency.success === false }"></i>{{ latency.name }}</small>
                  <strong :class="{ failed: latency.success === false }">{{ latencyText(latency.success, latency.latency_ms, latency.error_class) }}</strong>
                </div>
                <div v-if="!item.latency?.length" class="latency-item empty-item"><small><i></i>{{ item.node.latency_mode === 'tcping' ? 'TCPing' : 'Ping' }}</small><strong>等待后台分配目标</strong></div>
              </div>
            </section>

            <div v-if="item.node.custom_badges?.length || item.node.custom_links?.length || item.node.custom_html" class="custom-display">
              <div v-if="item.node.custom_badges?.length" class="custom-badges"><span v-for="badge in item.node.custom_badges" :key="`${badge.label}-${badge.color}`" :class="`custom-badge ${badge.color}`">{{ badge.label }}</span></div>
              <div v-if="item.node.custom_links?.length" class="custom-links" @click.stop><a v-for="link in item.node.custom_links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }}</a></div>
              <div v-if="item.node.custom_html" class="custom-html" @click.stop v-html="item.node.custom_html"></div>
            </div>

            <footer>
              <span>{{ osName(item) }}</span>
              <time>{{ item.report?.captured_at ? `最后更新：${new Date(item.report.captured_at).toLocaleString('zh-CN', { hour12: false })}` : '暂无上报数据' }}</time>
            </footer>
          </div>
        </article>
      </section>
    </main>

    <div v-if="chartNode" class="chart-overlay" @click.self="closeHistory">
      <section class="chart-dialog" role="dialog" aria-modal="true" :aria-label="`${chartNode.node.name} 历史图表`">
        <header>
          <div><small>节点历史</small><strong>{{ chartNode.node.name }}</strong></div>
          <button type="button" aria-label="关闭历史图表" @click="closeHistory">×</button>
        </header>
        <nav class="range-switch" aria-label="历史时间范围">
          <button v-for="item in historyRanges" :key="item" type="button" :class="{ active: chartRange === item }" @click="chartRange = item; loadHistory()">{{ item }}</button>
        </nav>
        <p v-if="chartError" class="chart-message error">{{ chartError }}</p>
        <p v-else-if="chartLoading" class="chart-message">正在读取并聚合历史数据…</p>
        <div class="chart-block">
          <h3>资源与实时速率</h3>
          <div ref="resourceChartElement" class="chart-canvas"></div>
        </div>
        <div class="chart-block">
          <h3>Ping / TCPing 延迟</h3>
          <div ref="latencyChartElement" class="chart-canvas"></div>
        </div>
        <div class="chart-block">
          <h3>上传 / 下载 / 总流量累计</h3>
          <div ref="trafficChartElement" class="chart-canvas"></div>
        </div>
      </section>
    </div>

    <footer class="site-footer">© {{ now.getFullYear() }} MyProbe · 自托管服务器监控</footer>
  </div>
</template>
