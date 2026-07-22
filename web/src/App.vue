<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { connectRealtime, fetchHistory, fetchNodes } from './api'
import type { HistoryRange, HistoryResponse, PublicNode, RealtimeEvent } from './types'

type Theme = 'light' | 'dark'

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
const historyRanges: HistoryRange[] = ['1h', '12h', '1d', '3d', '7d', '30d']
const initialTheme = localStorage.getItem('myprobe-theme') as Theme | null
const theme = ref<Theme>(initialTheme ?? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'))
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
  return days ? `${days}天 ${hours}时` : hours ? `${hours}时 ${minutes}分` : `${minutes}分钟`
}

function flag(code: string) {
  const normalized = code?.toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return '🌐'
  return String.fromCodePoint(...[...normalized].map((letter) => 127397 + letter.charCodeAt(0)))
}

function maskedIP(value?: string) {
  if (!value) return '—'
  const parts = value.split('.')
  return parts.length === 4 ? `${parts[0]}.${parts[1]}.••.••` : value.replace(/:[^:]+$/, ':••••')
}

function percent(value = 0) {
  return `${Math.max(0, Math.min(100, value)).toFixed(value >= 10 ? 0 : 1)}%`
}

function barClass(value = 0) {
  return value >= 90 ? 'danger' : value >= 75 ? 'warning' : ''
}

function price(item: PublicNode) {
  if (item.node.price_minor == null || !item.node.currency) return '未设置价格'
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: item.node.currency }).format(item.node.price_minor / 100)
}

function expiry(item: PublicNode) {
  if (!item.node.expires_at) return '无到期日期'
  const days = Math.ceil((new Date(item.node.expires_at).getTime() - now.value.getTime()) / 86400000)
  return days >= 0 ? `剩 ${days} 天` : `已过期 ${Math.abs(days)} 天`
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
          <span>MyProbe</span>
          <small>服务器探针</small>
        </a>
        <div class="nav-actions">
          <span class="connection-state" :class="{ online: connected }">
            <i></i>{{ connected ? '实时' : '重连中' }}
          </span>
          <button class="soft-button" type="button" @click="toggleTheme">
            <span aria-hidden="true">{{ theme === 'light' ? '☾' : '☀' }}</span>
            {{ theme === 'light' ? '暗色' : '亮色' }}
          </button>
          <a class="soft-button admin-link" href="/admin">后台</a>
        </div>
      </div>
    </header>

    <main>
      <section class="overview-grid" aria-label="总览">
        <article class="overview-card accent-blue">
          <span class="overview-label">当前时间</span>
          <strong class="clock">{{ now.toLocaleTimeString('zh-CN', { hour12: false }) }}</strong>
          <small>{{ now.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }) }}</small>
        </article>
        <article class="overview-card accent-green">
          <span class="overview-label">服务器概况</span>
          <strong><b class="dot online"></b>{{ onlineCount }} <em>/</em> <b class="dot offline"></b>{{ visibleNodes.length - onlineCount }}</strong>
          <small>在线 / 离线</small>
        </article>
        <article class="overview-card accent-purple">
          <span class="overview-label">总流量概览</span>
          <strong class="metric-pair"><span>↑ {{ formatBytes(totalTraffic.up) }}</span><span>↓ {{ formatBytes(totalTraffic.down) }}</span></strong>
          <small>上传 / 下载</small>
        </article>
        <article class="overview-card accent-orange">
          <span class="overview-label">实时速率</span>
          <strong class="metric-pair"><span>↑ {{ formatBytes(totalRate.up, '/s') }}</span><span>↓ {{ formatBytes(totalRate.down, '/s') }}</span></strong>
          <small>当前筛选节点</small>
        </article>
      </section>

      <section class="filter-bar" aria-label="标签筛选">
        <button :class="{ active: activeTag === '__all__' }" @click="activeTag = '__all__'">全部 <span>{{ nodes.length }}</span></button>
        <button v-for="([tag, count]) in tags" :key="tag" :class="{ active: activeTag === tag }" @click="activeTag = tag">
          {{ tag }} <span>{{ count }}</span>
        </button>
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

      <section v-else class="node-grid" aria-live="polite">
        <article v-for="item in visibleNodes" :key="item.node.id" class="node-card" :class="{ offline: !item.online, stale: item.stale }">
          <div class="card-glow"></div>
          <header class="node-header">
            <div class="node-title">
              <span class="flag">{{ flag(item.node.country_code) }}</span>
              <div><strong>{{ item.node.name }}</strong><small>{{ item.report?.cpu.architecture || '等待 Agent 上报' }}</small></div>
            </div>
            <span class="status-dot" :class="{ online: item.online }" :title="item.online ? '在线' : '离线'"></span>
          </header>

          <div class="commercial-row">
            <span>{{ price(item) }}<template v-if="item.node.billing_cycle">/{{ item.node.billing_cycle }}</template></span>
            <span :class="{ overdue: item.node.expires_at && new Date(item.node.expires_at) < now }">{{ expiry(item) }}</span>
          </div>

          <div v-if="item.node.custom_badges?.length || item.node.custom_links?.length || item.node.custom_html" class="custom-display">
            <div v-if="item.node.custom_badges?.length" class="custom-badges"><span v-for="badge in item.node.custom_badges" :key="`${badge.label}-${badge.color}`" :class="`custom-badge ${badge.color}`">{{ badge.label }}</span></div>
            <div v-if="item.node.custom_links?.length" class="custom-links"><a v-for="link in item.node.custom_links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }} ↗</a></div>
            <div v-if="item.node.custom_html" class="custom-html" v-html="item.node.custom_html"></div>
          </div>

          <div class="config-line">
            <span>{{ item.report?.cpu.logical_cores || '—' }}C</span>
            <i></i><span>{{ formatBytes(item.report?.memory.total_bytes || 0) }}</span>
            <i></i><span>{{ formatBytes(aggregate(item).diskTotal) }}</span>
          </div>

          <div class="meta-grid">
            <div><span>网络</span><strong>{{ maskedIP(item.report?.public_ip) }}</strong></div>
            <div><span>速率</span><strong>↑ {{ formatBytes(aggregate(item).txRate, '/s') }} · ↓ {{ formatBytes(aggregate(item).rxRate, '/s') }}</strong></div>
            <div><span>运行</span><strong>{{ formatUptime(item.report?.uptime_seconds) }}</strong></div>
            <div><span>流量</span><strong>↑ {{ formatBytes(aggregate(item).txTotal) }} · ↓ {{ formatBytes(aggregate(item).rxTotal) }}</strong></div>
            <div><span>周期</span><strong>{{ item.node.traffic_reset_day ? `每月 ${item.node.traffic_reset_day} 日` : '自然月' }}</strong></div>
            <div><span>本周期</span><strong>↑ {{ formatBytes(item.traffic?.tx_bytes || 0) }} · ↓ {{ formatBytes(item.traffic?.rx_bytes || 0) }}</strong></div>
          </div>

          <div class="divider"></div>
          <div class="stat-list">
            <div class="stat-row">
              <span>CPU</span><div class="bar"><i :class="barClass(item.report?.cpu.usage_percent)" :style="{ width: percent(item.report?.cpu.usage_percent) }"></i></div><strong>{{ percent(item.report?.cpu.usage_percent) }}</strong>
            </div>
            <div class="stat-row">
              <span>内存</span><div class="bar"><i :class="barClass(item.report?.memory.usage_percent)" :style="{ width: percent(item.report?.memory.usage_percent) }"></i></div><strong>{{ percent(item.report?.memory.usage_percent) }}</strong>
            </div>
            <div class="stat-row">
              <span>硬盘</span><div class="bar"><i :class="barClass(aggregate(item).diskPercent)" :style="{ width: percent(aggregate(item).diskPercent) }"></i></div><strong>{{ percent(aggregate(item).diskPercent) }}</strong>
            </div>
          </div>

          <div class="latency-panel" :class="{ empty: !item.latency?.length }">
            <div v-for="latency in item.latency" :key="latency.target_id">
              <span>{{ latency.kind === 'tcping' ? 'TCP' : 'PING' }}</span>
              <b>{{ latency.name }}</b>
              <strong :class="{ failed: latency.success === false }">{{ latencyText(latency.success, latency.latency_ms, latency.error_class) }}</strong>
            </div>
            <div v-if="!item.latency?.length"><span>{{ item.node.latency_mode === 'tcping' ? 'TCP' : 'PING' }}</span><b>延迟</b><strong>等待后台分配目标</strong></div>
          </div>

          <footer>
            <span>{{ item.report?.cpu.model || '尚未连接' }}</span>
            <button type="button" @click="openHistory(item)">历史图表</button>
            <time>{{ item.report?.captured_at ? `更新 ${new Date(item.report.captured_at).toLocaleTimeString('zh-CN', { hour12: false })}` : '无数据' }}</time>
          </footer>
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
