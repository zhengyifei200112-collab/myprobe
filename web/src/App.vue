<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { connectRealtime, fetchHistory, fetchNodes } from './api'
import { DsCard, DsEmptyState, DsLoading, DsTabs } from './design-system'
import PublicNodeCard from './public-dashboard/PublicNodeCard.vue'
import { aggregateNode as aggregate, commonByteUnit, formatBytesInUnit, formatMilliseconds, formatPercent } from './public-dashboard/metrics'
import { applyAppearance, backgroundVariables, cacheAppearance } from './appearance'
import { defaultSiteSettings, normalizeSiteSettings, type HistoryRange, type HistoryResponse, type PublicNode, type RealtimeEvent, type SiteSettings, type ThemeMode } from './types'

type DisplayMode = 'compact' | 'detailed'

const nodes = ref<PublicNode[]>([])
const siteSettings = ref<SiteSettings>(defaultSiteSettings())
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
const chartDialogElement = ref<HTMLElement>()
const historyRanges: HistoryRange[] = ['1h', '12h', '1d', '3d', '7d', '30d', '1y']
const initialTheme = localStorage.getItem('myprobe-theme') as ThemeMode | null
const theme = ref<ThemeMode>(['light', 'dark', 'system'].includes(initialTheme || '') ? initialTheme! : 'system')
const initialDisplayMode = localStorage.getItem('myprobe-display-mode') as DisplayMode | null
const displayMode = ref<DisplayMode>(initialDisplayMode === 'detailed' ? 'detailed' : 'compact')
let disconnect: (() => void) | undefined
let clock: number | undefined
let resourceChart: any
let latencyChart: any
let trafficChart: any
let historyTrigger: HTMLElement | null = null

const sortedNodes = computed(() => [...nodes.value].sort((a, b) => a.node.sort_order - b.node.sort_order || a.node.name.localeCompare(b.node.name)))
const tags = computed(() => {
  const counts = new Map<string, number>()
  for (const item of nodes.value) for (const tag of item.node.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1)
  return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b, 'zh-CN', { numeric: true }))
})
const filterTabs = computed(() => [
  { value: '__all__', label: `全部 ${nodes.value.length}` },
  ...tags.value.map(([tag, count]) => ({ value: tag, label: `${tag} ${count}` })),
])
const visibleNodes = computed(() => activeTag.value === '__all__'
  ? sortedNodes.value
  : sortedNodes.value.filter((item) => item.node.tags?.includes(activeTag.value)))
const onlineCount = computed(() => visibleNodes.value.filter((item) => item.online).length)
const totalRate = computed(() => sumNetwork(visibleNodes.value, 'rate'))
const totalTraffic = computed(() => sumNetwork(visibleNodes.value, 'total'))
const totalRateUnit = computed(() => commonByteUnit([totalRate.value.up, totalRate.value.down]))
const totalTrafficUnit = computed(() => commonByteUnit([totalTraffic.value.up, totalTraffic.value.down]))
const publicBackgroundStyle = computed(() => ({ ...backgroundVariables(siteSettings.value.public_background), '--site-has-background': siteSettings.value.public_background.url ? '1' : '0' }))
const themeAction = computed(() => theme.value === 'light' ? '深色' : theme.value === 'dark' ? '系统' : '浅色')

function mergeEvent(event: RealtimeEvent) {
  if (event.type === 'snapshot') {
    nodes.value = event.nodes
    if (event.settings) {
      siteSettings.value = normalizeSiteSettings(event.settings)
      cacheAppearance(event.settings)
      applyAppearance(event.settings, theme.value)
    }
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
    siteSettings.value = normalizeSiteSettings(response.settings ?? siteSettings.value)
    if (!initialTheme) theme.value = siteSettings.value.theme_mode
    cacheAppearance(siteSettings.value)
    applyAppearance(siteSettings.value, theme.value)
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
  theme.value = theme.value === 'light' ? 'dark' : theme.value === 'dark' ? 'system' : 'light'
  applyAppearance(siteSettings.value, theme.value)
  localStorage.setItem('myprobe-theme', theme.value)
  if (chartNode.value) void loadHistory()
}

function toggleDisplayMode() {
  displayMode.value = displayMode.value === 'compact' ? 'detailed' : 'compact'
  localStorage.setItem('myprobe-display-mode', displayMode.value)
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

async function openHistory(item: PublicNode) {
  historyTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  chartNode.value = item
  chartRange.value = '1h'
  await nextTick()
  chartDialogElement.value?.querySelector<HTMLButtonElement>('button')?.focus()
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
  const rateUnit = commonByteUnit(history.metrics.flatMap((point) => [point.tx_bytes_per_second, point.rx_bytes_per_second]))
  const rateValue = (value: number) => formatBytesInUnit(value, rateUnit, '/s')
  resourceChart.setOption({
    ...common,
    tooltip: { ...common.tooltip },
    yAxis: [
      { type: 'value', min: 0, max: 100, axisLabel: { color: text, formatter: '{value}%' }, splitLine: { lineStyle: { color: border } } },
      { type: 'value', min: 0, name: `单位：${rateUnit.label}/s`, nameTextStyle: { color: text }, axisLabel: { color: text, formatter: rateValue }, splitLine: { show: false } },
    ],
    series: [
      { name: 'CPU', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.cpu_percent]), tooltip: { valueFormatter: formatPercent }, lineStyle: { color: blue }, itemStyle: { color: blue } },
      { name: '内存', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.memory_percent]), tooltip: { valueFormatter: formatPercent }, lineStyle: { color: cyan }, itemStyle: { color: cyan } },
      { name: '硬盘', type: 'line', showSymbol: false, smooth: true, data: history.metrics.map((p) => [p.time, p.disk_percent]), tooltip: { valueFormatter: formatPercent }, lineStyle: { color: purple }, itemStyle: { color: purple } },
      { name: '上传', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map((p) => [p.time, p.tx_bytes_per_second]), tooltip: { valueFormatter: rateValue }, lineStyle: { color: orange }, itemStyle: { color: orange } },
      { name: '下载', type: 'line', yAxisIndex: 1, showSymbol: false, data: history.metrics.map((p) => [p.time, p.rx_bytes_per_second]), tooltip: { valueFormatter: rateValue }, lineStyle: { color: green }, itemStyle: { color: green } },
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
    series: [...targets.values()].map((target) => ({ name: target.name, type: 'line', connectNulls: false, showSymbol: false, smooth: true, data: target.points, tooltip: { valueFormatter: formatMilliseconds } })),
  })
  const trafficUnit = commonByteUnit(history.traffic.map((point) => point.total_bytes))
  const trafficValue = (value: number) => formatBytesInUnit(value, trafficUnit)
  trafficChart.setOption({
    ...common,
    tooltip: { ...common.tooltip, valueFormatter: trafficValue },
    yAxis: { type: 'value', min: 0, name: `单位：${trafficUnit.label}`, nameTextStyle: { color: text }, axisLabel: { color: text, formatter: trafficValue }, splitLine: { lineStyle: { color: border } } },
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
  void nextTick(() => {
    historyTrigger?.focus()
    historyTrigger = null
  })
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
  <div class="app-shell site-background" :style="publicBackgroundStyle">
    <header class="navbar">
      <div class="navbar-inner">
        <a class="brand" href="/" :aria-label="`${siteSettings.site_name || 'MyProbe'} 首页`">
          <img v-if="siteSettings.logo_url" class="brand-logo" :src="siteSettings.logo_url" alt="">
          <span v-else class="brand-mark">MP</span>
          <span class="brand-copy">
            <span class="brand-title">{{ siteSettings.site_name || 'MyProbe' }}</span>
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
          <button class="soft-button" type="button" :aria-label="`切换到${themeAction}主题`" @click="toggleTheme">
            <span class="button-icon" aria-hidden="true">
              <svg v-if="theme === 'light'" viewBox="0 0 16 16"><circle cx="8" cy="8" r="2.6" /><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.05 3.05l1.4 1.4M11.55 11.55l1.4 1.4M12.95 3.05l-1.4 1.4M4.45 11.55l-1.4 1.4" /></svg>
              <svg v-else viewBox="0 0 16 16"><path d="M13.5 10.6A6 6 0 0 1 5.4 2.5 6 6 0 1 0 13.5 10.6Z" /></svg>
            </span>
            {{ themeAction }}
          </button>
          <a class="soft-button admin-link" href="/admin"><span class="button-icon" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="M9 3h4v10H9M7 5l3 3-3 3M10 8H2" /></svg></span>后台</a>
        </div>
      </div>
    </header>

    <main>
      <section class="dashboard-intro" aria-labelledby="dashboard-title">
        <div>
          <div class="dashboard-eyebrow">Infrastructure overview</div>
          <h1 id="dashboard-title">{{ siteSettings.dashboard_title || siteSettings.site_title }}</h1>
          <p>{{ siteSettings.dashboard_description || siteSettings.site_description }}</p>
        </div>
        <div class="live-badge" :class="{ reconnecting: !connected }" :title="connected ? 'WebSocket 实时连接正常' : '正在重新连接实时数据'">
          <span class="live-dot" aria-hidden="true"></span>
          {{ connected ? '实时监控中' : '正在重连' }}
        </div>
      </section>

      <section v-if="siteSettings.header_html" class="site-custom-block site-custom-header" v-html="siteSettings.header_html"></section>

      <section class="overview-grid" aria-label="总览">
        <DsCard as="article" padding="medium" class="overview-card overview-time-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5l3.5 2"/></svg></span><span class="overview-title">当前时间</span></div>
          <div class="overview-content">
            <strong class="overview-value clock">{{ now.toLocaleTimeString('zh-CN', { hour12: false }) }}</strong>
            <small>{{ now.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }) }}</small>
          </div>
        </DsCard>
        <DsCard as="article" padding="medium" class="overview-card overview-status-card" :title="`当前筛选：总数 ${visibleNodes.length} • 在线 ${onlineCount} • 离线 ${visibleNodes.length - onlineCount}`">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="4.5" width="16" height="6" rx="2"/><rect x="4" y="13.5" width="16" height="6" rx="2"/><path d="M8 7.5h.01M8 16.5h.01M12 7.5h5M12 16.5h5"/></svg></span><span class="overview-title">服务器概况</span></div>
          <div class="overview-content">
            <div class="overview-main-row"><strong class="overview-main-number">{{ visibleNodes.length }}</strong><span>台节点</span></div>
            <div class="status-breakdown"><span><b class="dot online"></b>在线 <strong>{{ onlineCount }}</strong></span><span><b class="dot offline"></b>离线 <strong>{{ visibleNodes.length - onlineCount }}</strong></span></div>
          </div>
        </DsCard>
        <DsCard as="article" padding="medium" class="overview-card overview-traffic-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 19V9M10 19V5M15 19v-7M20 19V7"/><path d="M3.5 19.5h18"/></svg></span><span class="overview-title">累计流量</span></div>
          <div class="overview-content overview-pairline">
            <span><small><i class="up-arrow">↑</i> 上传</small><strong class="overview-value">{{ formatBytesInUnit(totalTraffic.up, totalTrafficUnit) }}</strong></span>
            <span><small><i class="down-arrow">↓</i> 下载</small><strong class="overview-value">{{ formatBytesInUnit(totalTraffic.down, totalTrafficUnit) }}</strong></span>
          </div>
        </DsCard>
        <DsCard as="article" padding="medium" class="overview-card overview-speed-card">
          <div class="overview-head"><span class="overview-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 8.5h4l2.2-4 4.2 15 2.5-8h4.1"/></svg></span><span class="overview-title">实时速率</span></div>
          <div class="overview-content overview-pairline">
            <span><small><i class="up-arrow">↑</i> 上传</small><strong class="overview-value">{{ formatBytesInUnit(totalRate.up, totalRateUnit, '/s') }}</strong></span>
            <span><small><i class="down-arrow">↓</i> 下载</small><strong class="overview-value">{{ formatBytesInUnit(totalRate.down, totalRateUnit, '/s') }}</strong></span>
          </div>
        </DsCard>
      </section>

      <section class="nodes-section" aria-labelledby="nodes-title">
        <div class="nodes-toolbar">
          <div class="section-heading">
            <div><span class="section-kicker">Infrastructure</span><h2 id="nodes-title">节点列表</h2></div>
            <div class="section-counter"><strong>{{ visibleNodes.length }}</strong> 个节点</div>
          </div>
          <div class="filter-section"><DsTabs v-model="activeTag" :items="filterTabs" label="按标签筛选节点" /></div>
        </div>
      </section>

      <div v-if="error" class="notice">{{ error }}，当前展示最后缓存数据。</div>
      <DsLoading v-if="loading" label="正在读取节点" />
      <DsEmptyState v-else-if="visibleNodes.length === 0" title="还没有可显示的节点" description="在管理后台注册第一台服务器后，数据会实时出现在这里。">
        <template #icon><svg viewBox="0 0 24 24"><rect x="4" y="5" width="16" height="6" rx="2"/><rect x="4" y="14" width="16" height="5" rx="2"/><path d="M8 8h.01M8 16.5h.01"/></svg></template>
      </DsEmptyState>

      <section v-else class="node-grid" :class="displayMode" aria-live="polite">
        <PublicNodeCard
          v-for="item in visibleNodes"
          :key="item.node.id"
          :item="item"
          :display-mode="displayMode"
          :now="now"
          @open="openHistory(item)"
        />
      </section>
    </main>

    <div v-if="chartNode" class="chart-overlay" @click.self="closeHistory" @keydown.esc="closeHistory">
      <section ref="chartDialogElement" class="chart-dialog" role="dialog" aria-modal="true" :aria-label="`${chartNode.node.name} 历史图表`">
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

    <section v-if="siteSettings.footer_html" class="site-custom-block site-custom-footer" v-html="siteSettings.footer_html"></section>
    <footer class="site-footer">
      <span>{{ siteSettings.copyright_text || `© ${now.getFullYear()} ${siteSettings.site_name || 'MyProbe'} · 自托管服务器监控` }}</span>
      <span v-if="siteSettings.footer_text">{{ siteSettings.footer_text }}</span>
      <span v-if="siteSettings.contact">{{ siteSettings.contact }}</span>
      <nav v-if="siteSettings.github_url || siteSettings.blog_url || siteSettings.custom_links.length" aria-label="站点链接"><a v-if="siteSettings.github_url" :href="siteSettings.github_url" target="_blank" rel="noopener noreferrer">GitHub</a><a v-if="siteSettings.blog_url" :href="siteSettings.blog_url" target="_blank" rel="noopener noreferrer">博客</a><a v-for="link in siteSettings.custom_links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }}</a></nav>
    </footer>
  </div>
</template>
