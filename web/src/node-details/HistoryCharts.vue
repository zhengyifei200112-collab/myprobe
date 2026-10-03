<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { HistoryResponse } from '../types'
import { DsButton } from '../design-system'
import { commonByteUnit } from '../public-dashboard/metrics'
import { withGaps } from './history-view'

const props = defineProps<{ history: HistoryResponse; theme: string }>()
const sections = [
  { key: 'resources', title: '资源使用率', description: 'CPU、内存和磁盘占比', empty: '此范围内没有资源样本' },
  { key: 'rates', title: '网络速率', description: '上传与下载速率', empty: '此范围内没有速率样本' },
  { key: 'latency', title: '网络延迟', description: '观测方向：当前节点 → 探测目标', empty: '此范围内没有延迟样本' },
  { key: 'traffic', title: '累计观测流量', description: '选定范围内已观测到的计数器增量', empty: '至少需要两次计数器观测或已有汇总数据' },
]
const root = ref<HTMLElement>()
let charts: any[] = []
let engine: typeof import('../charting').default | undefined
let generation = 0
let observer: ResizeObserver | undefined
const group = `node-history-${Math.random().toString(36).slice(2)}`
const systemTheme = matchMedia('(prefers-color-scheme: dark)')

function hasData(key: string) {
  return key === 'latency' ? props.history.latency.length > 0 : key === 'traffic' ? props.history.traffic.length > 0 : props.history.metrics.length > 0
}
function dispose() {
  engine?.disconnect(group)
  charts.forEach(chart => chart.dispose())
  charts = []
}
function zoom(start: number, end: number) {
  charts.forEach(chart => chart.dispatchAction({ type: 'dataZoom', start, end }))
}
async function render() {
  const version = ++generation
  await nextTick()
  const { default: echarts } = await import('../charting')
  if (version !== generation || !root.value) return
  engine = echarts
  dispose()
  const h = props.history
  const style = getComputedStyle(document.documentElement)
  const color = (name: string) => style.getPropertyValue(name).trim()
  const rateUnit = commonByteUnit(h.metrics.flatMap(p => [p.rx_bytes_per_second, p.tx_bytes_per_second]))
  const trafficUnit = commonByteUnit(h.traffic.map(p => p.total_bytes))
  const line = (name: string, points: Array<[string, number | null]>) => ({ name, type: 'line', showSymbol: true, symbolSize: 3, connectNulls: false, smooth: false, data: withGaps(points, h.bucket_seconds) })
  const targets = [...new Set(h.latency.map(p => p.target_id))]
  const series = [
    [line('CPU', h.metrics.map(p => [p.time, p.cpu_percent])), line('内存', h.metrics.map(p => [p.time, p.memory_percent])), line('磁盘', h.metrics.map(p => [p.time, p.disk_percent]))],
    [line('上传', h.metrics.map(p => [p.time, p.tx_bytes_per_second / rateUnit.divisor])), line('下载', h.metrics.map(p => [p.time, p.rx_bytes_per_second / rateUnit.divisor]))],
    targets.map(id => { const points = h.latency.filter(p => p.target_id === id); return line(`${points[0].kind === 'ping' ? 'Ping' : 'TCP'} · ${points[0].name}`, points.map(p => [p.time, p.latency_ms ?? null])) }),
    [line('上传累计', h.traffic.map(p => [p.time, p.tx_bytes / trafficUnit.divisor])), line('下载累计', h.traffic.map(p => [p.time, p.rx_bytes / trafficUnit.divisor]))],
  ]
  const units = ['%', `${rateUnit.label}/s`, 'ms', trafficUnit.label]
  for (const [index, section] of sections.entries()) {
    const element = root.value.querySelector<HTMLElement>(`[data-chart="${section.key}"]`)
    if (!element || !hasData(section.key)) continue
    const chart = echarts.init(element)
    chart.group = group
    chart.setOption({
      animation: false, color: [color('--blue'), color('--cyan'), color('--purple'), color('--orange'), color('--green')],
      textStyle: { color: color('--text'), fontFamily: 'inherit' },
      tooltip: { trigger: 'axis', renderMode: 'richText', confine: true, valueFormatter: (value: number | null) => value == null ? '无数据' : `${Number(value).toFixed(2)} ${units[index]}` },
      legend: { type: 'scroll', top: 0, textStyle: { color: color('--text') } },
      grid: { left: 65, right: 20, top: 50, bottom: 65 },
      xAxis: { type: 'time', min: Math.floor(Date.parse(h.start) / (h.bucket_seconds * 1000)) * h.bucket_seconds * 1000, max: Date.parse(h.end), splitNumber: 3, axisLabel: { hideOverlap: true, color: color('--muted') } },
      yAxis: { type: 'value', min: 0, max: index === 0 ? 100 : undefined, name: units[index], axisLabel: { color: color('--muted') }, splitLine: { lineStyle: { color: color('--border') } } },
      dataZoom: [{ type: 'inside', filterMode: 'none' }, { type: 'slider', height: 20, bottom: 8, filterMode: 'none', textStyle: { color: color('--muted') } }],
      series: series[index],
    })
    charts.push(chart)
  }
  echarts.connect(group)
}
watch(() => [props.history, props.theme], () => void render())
onMounted(() => {
  observer = new ResizeObserver(() => charts.forEach(chart => chart.resize()))
  if (root.value) observer.observe(root.value)
  systemTheme.addEventListener('change', render)
  void render()
})
onBeforeUnmount(() => { generation++; observer?.disconnect(); systemTheme.removeEventListener('change', render); dispose() })
</script>

<template>
  <div ref="root" class="detail-charts">
    <div class="detail-chart-actions"><DsButton @click="zoom(25, 75)">放大中间时段</DsButton><DsButton @click="zoom(0, 100)">重置缩放</DsButton></div>
    <section v-for="section in sections" :key="section.key" class="detail-card">
      <h2>{{ section.title }}</h2><p class="detail-muted">{{ section.description }}</p>
      <div v-if="hasData(section.key)" :data-chart="section.key" class="detail-chart" role="img" :aria-label="`${section.title}历史图表，可使用缩放按钮调整范围`"></div>
      <p v-else class="detail-empty">{{ section.empty }}</p>
    </section>
  </div>
</template>
