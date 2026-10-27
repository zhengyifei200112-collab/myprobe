<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { DsDialog } from '../design-system'
import { QualityRequestError, qualityRequest, targetKey, type QualityObserver, type QualityResponse, type QualityTargetsPage, type QualityTarget, type QualityBounds } from './api'
import { customRange, lastEvening, localInput } from './time'

const props = defineProps<{ observers: QualityObserver[]; apiBase: string }>()
const emit = defineEmits<{ close: []; expired: [] }>()
const selected = ref<string[]>(props.observers.slice(0, 2).map(n => n.id))
const observerSearch = ref('')
const filteredObservers = computed(() => { const q = observerSearch.value.trim().toLocaleLowerCase(); return props.observers.filter(n => !q || n.name.toLocaleLowerCase().includes(q) || n.id.includes(q)) })
const targets = ref<QualityTarget[]>([]), selectedTarget = ref(''), nextCursor = ref('')
const range = ref('1h'), zone = ref(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
const customStart = ref(localInput(Date.now() - 3600000, zone.value)), customEnd = ref(localInput(Date.now() - 60000, zone.value))
const result = ref<QualityResponse>(), error = ref(''), notice = ref(''), busy = ref(false), moreBusy = ref(false)
const dirty = ref(false), targetsDirty = ref(true), chartElement = ref<HTMLElement>(), hidden = ref<string[]>([])
const colors = ['#007aff', '#9950cf', '#008f70', '#ba6c00', '#cb3c74', '#267fbb', '#846246', '#5d7381']
const ranges = ['1h', '12h', '1d', '3d', '7d', '30d', '1y']
let alive = true, revision = 0, controller: AbortController | undefined, moreController: AbortController | undefined, chart: any, echarts: any
let resizeObserver: ResizeObserver | undefined, themeObserver: MutationObserver | undefined
const hasRTT = computed(() => result.value?.nodes.some(n => n.points.some(p => p.rtt_mean_ms !== null)))
const targetLabel = computed(() => result.value ? `${result.value.target.name} · ${result.value.target.kind === 'ping' ? 'ICMP 探测' : 'TCP 连接'}` : '')
function changed(nodes = false) {
  revision++; controller?.abort(); moreController?.abort(); busy.value = false; result.value = undefined; chart?.dispose(); chart = undefined; error.value = ''; dirty.value = true
  if (nodes) targetsDirty.value = true
}
function choose(id: string, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  if (checked && selected.value.length >= 8) { (event.target as HTMLInputElement).checked = false; return }
  selected.value = checked ? [...selected.value, id] : selected.value.filter(v => v !== id)
  changed(true)
}
function firstEight() { selected.value = filteredObservers.value.slice(0, 8).map(n => n.id); changed(true) }
watch(() => props.observers.map(n => n.id).join('\0'), () => {
  const ids = new Set(props.observers.map(n => n.id)), retained = selected.value.filter(id => ids.has(id))
  if (retained.length !== selected.value.length) { selected.value = retained; changed(true) }
})
const nodeQuery = () => { const q = new URLSearchParams(); selected.value.forEach(id => q.append('node_id', id)); return q }
async function discover(signal: AbortSignal, reset: boolean) {
  const page = await qualityRequest<QualityTargetsPage>(props.apiBase, 'targets', nodeQuery(), signal)
  if (signal.aborted || !alive) return Date.parse(page.observed_at)
  if (reset) {
    targets.value = page.targets; nextCursor.value = page.next_cursor
    if (!targets.value.some(t => targetKey(t) === selectedTarget.value)) selectedTarget.value = targets.value[0] ? targetKey(targets.value[0]) : ''
    targetsDirty.value = false
  }
  return Date.parse(page.observed_at)
}
async function moreTargets() {
  if (!nextCursor.value || busy.value || moreBusy.value || targetsDirty.value) return
  const token = revision, request = new AbortController(); moreController = request; moreBusy.value = true
  try {
    const q = nodeQuery(); q.set('cursor', nextCursor.value)
    const page = await qualityRequest<QualityTargetsPage>(props.apiBase, 'targets', q, request.signal)
    if (alive && token === revision) { const seen = new Set(targets.value.map(targetKey)); targets.value.push(...page.targets.filter(t => !seen.has(targetKey(t)))); nextCursor.value = page.next_cursor }
  } catch (e) { if (alive && token === revision && !request.signal.aborted) { error.value = e instanceof TypeError ? '网络请求未完成，请重试。' : e instanceof Error ? e.message : '目标加载失败。'; if (e instanceof QualityRequestError && e.status === 401) emit('expired') } }
  finally { if (alive) moreBusy.value = false }
}
async function compare() {
  if (!selected.value.length) { error.value = '请至少选择一个观测节点。'; return }
  controller?.abort(); controller = new AbortController(); const signal = controller.signal, token = ++revision
  busy.value = true; error.value = ''; notice.value = ''; result.value = undefined; chart?.dispose(); chart = undefined
  try {
    new Intl.DateTimeFormat('zh-CN', { timeZone: zone.value }).format()
    let serverNow = Date.now()
    if (targetsDirty.value || !targets.value.length || range.value === 'peak' || range.value === 'custom') serverNow = await discover(signal, targetsDirty.value || !targets.value.length)
    if (!alive || token !== revision || signal.aborted) return
    const target = targets.value.find(t => targetKey(t) === selectedTarget.value)
    if (!target) { notice.value = '所选节点暂无可比较目标，请由管理员配置或分配探测目标。'; return }
    const q = nodeQuery(); q.set('target_id', target.id); q.set('kind', target.kind); q.set('timezone', zone.value)
    if (range.value === 'peak' || range.value === 'custom') {
      const interval = range.value === 'peak' ? lastEvening(serverNow, zone.value) : customRange(customStart.value, customEnd.value, zone.value, serverNow)
      q.set('start', interval.start); q.set('end', interval.end)
    } else q.set('range', range.value)
    const data = await qualityRequest<QualityResponse>(props.apiBase, 'series', q, signal)
    if (data.nodes.length > 8 || data.nodes.some(n => n.points.length > 2000)) throw new Error('返回数据超出可显示范围，请缩小范围后重试。')
    if (!alive || token !== revision) return
    result.value = data; hidden.value = []; dirty.value = false
    await nextTick(); await render()
  } catch (e) {
    if (alive && token === revision && !signal.aborted) {
      error.value = e instanceof RangeError ? '时区无效，请使用 UTC 或有效时区名称。' : e instanceof TypeError ? '网络请求未完成，请重试。' : e instanceof Error ? e.message : '查询失败，请重试。'
      if (e instanceof QualityRequestError && e.status === 401) emit('expired')
    }
    if (e instanceof QualityRequestError && e.status === 404) targetsDirty.value = true
  } finally { if (alive && token === revision) busy.value = false }
}
function textTime(value: string | null | undefined, timeZone = result.value?.timezone || zone.value) {
  if (!value || !Number.isFinite(Date.parse(value))) return '未知'
  return new Intl.DateTimeFormat('zh-CN', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(value))
}
const number = (v: number | null | undefined, digits = 1) => typeof v === 'number' && Number.isFinite(v) ? v.toLocaleString('zh-CN', { maximumFractionDigits: digits }) : '—'
function percentile(v: number | null, method: string) { return `${v === null ? '' : method === 'histogram_v1' ? '≈ ' : ''}${number(v)}` }
const bounds = (b: QualityBounds | null) => b ? `分位数位于 ${number(b.lower_ms, 3)}—${number(b.upper_ms, 3)} ms` : '原始样本精确分位数；无成功样本时不可用'
function toggle(id: string) { hidden.value = hidden.value.includes(id) ? hidden.value.filter(v => v !== id) : [...hidden.value, id]; void render() }
async function render() {
  if (!alive || !result.value || !chartElement.value) return
  if (!echarts) echarts = (await import('../charting')).default
  if (!alive || !result.value || !chartElement.value) return
  chart ??= echarts.init(chartElement.value)
  const data = result.value, style = getComputedStyle(document.documentElement), color = style.getPropertyValue('--text-secondary').trim() || '#666', border = style.getPropertyValue('--border').trim() || '#ddd'
  const visible = data.nodes.filter(n => !hidden.value.includes(n.node_id))
  const failed = new Map<string, string>()
  for (const node of visible) for (const p of node.points) if ((p.counts.failure ?? 0) > 0) failed.set(p.time, p.end)
  const areas = [...failed].map(([a, b]) => [{ xAxis: Date.parse(a) }, { xAxis: Date.parse(b) }])
  const fields = ['rtt_mean_ms', 'execution_failure_pct', 'coverage_pct'] as const
  const series = fields.flatMap((field, grid) => data.nodes.map((n, i) => ({
    name: `${i + 1}. ${n.name}`, type: 'line', xAxisIndex: grid, yAxisIndex: grid, showSymbol: false, connectNulls: false, smooth: false,
    lineStyle: { width: 1.7, color: colors[i] }, itemStyle: { color: colors[i] },
    data: hidden.value.includes(n.node_id) ? [] : n.points.map(p => [Date.parse(p.time), p[field]]),
    ...(grid === 0 && i === 0 ? { markArea: { silent: true, itemStyle: { color: 'rgba(225,65,65,0.08)' }, data: areas } } : {}),
  })))
  chart.setOption({ animation: false, textStyle: { fontFamily: 'system-ui', color },
    grid: [{ left: 54, right: 14, top: 32, height: 170 }, { left: 54, right: 14, top: 250, height: 100 }, { left: 54, right: 14, top: 398, height: 100 }],
    xAxis: fields.map((_, i) => ({ type: 'time', gridIndex: i, min: Date.parse(data.requested_start), max: Date.parse(data.requested_end), axisLabel: { show: i === 2, color, hideOverlap: true, formatter: (v: number) => new Intl.DateTimeFormat('zh-CN', { timeZone: data.timezone, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(v) }, axisLine: { lineStyle: { color: border } }, splitLine: { show: false } })),
    yAxis: ['平均 RTT · ms', '执行失败率 · %', '覆盖率 · %'].map((name, i) => ({ type: 'value', gridIndex: i, name, min: 0, ...(i ? { max: 100 } : {}), nameTextStyle: { color, align: 'left' }, axisLabel: { color }, splitLine: { lineStyle: { color: border, type: 'dashed' } } })),
    axisPointer: { link: [{ xAxisIndex: 'all' }] },
    tooltip: { trigger: 'axis', renderMode: 'richText', confine: true, textStyle: { fontSize: 11 }, formatter: (items: any[]) => {
      if (!items.length) return ''
      const at = Number(items[0].axisValue), index = items[0].dataIndex
      if (!Number.isFinite(at)) return ''
      return `${textTime(new Date(at).toISOString(), data.timezone)}\n${visible.map(n => {
        const p = n.points[index]
        return `${n.name.slice(0, 17)}\n RTT ${number(p?.rtt_mean_ms)} ms  失败 ${number(p?.execution_failure_pct)}%  覆盖 ${number(p?.coverage_pct)}%`
      }).join('\n')}`
    } }, series,
  }, { notMerge: true })
}
onMounted(() => {
  resizeObserver = new ResizeObserver(() => chart?.resize()); if (chartElement.value) resizeObserver.observe(chartElement.value)
  themeObserver = new MutationObserver(() => void render()); themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  void compare()
})
watch(chartElement, (el, previous) => { if (previous) resizeObserver?.unobserve(previous); if (el) resizeObserver?.observe(el) })
onBeforeUnmount(() => { alive = false; revision++; controller?.abort(); moreController?.abort(); resizeObserver?.disconnect(); themeObserver?.disconnect(); chart?.dispose() })
</script>

<template>
  <DsDialog :open="true" size="wide" title="网络质量对比" description="观测节点 → 探测目标，最多比较 8 个节点。" @close="emit('close')">
    <section class="network-quality">
      <fieldset class="quality-observers"><legend>观测节点（{{ selected.length }}/8）</legend>
        <input v-model="observerSearch" class="quality-search" type="search" aria-label="搜索观测节点" placeholder="搜索节点名">
        <div class="quality-node-options"><label v-for="n in filteredObservers" :key="n.id"><input type="checkbox" :value="n.id" :checked="selected.includes(n.id)" :disabled="selected.length >= 8 && !selected.includes(n.id)" @change="choose(n.id, $event)"><span>{{ n.name }}</span></label><span v-if="!filteredObservers.length">没有匹配节点</span></div>
        <button type="button" class="soft-button" @click="firstEight">选择前 8 个</button> <button type="button" class="soft-button" @click="selected = []; changed(true)">清空选择</button>
      </fieldset>
      <form class="quality-controls" @submit.prevent="compare">
        <label>探测目标<select v-model="selectedTarget" aria-label="探测目标" @change="changed()"><option v-if="!targets.length" value="">{{ busy ? '正在读取目标…' : '暂无目标' }}</option><option v-for="t in targets" :key="targetKey(t)" :value="targetKey(t)">{{ t.name }} · {{ t.kind === 'ping' ? 'ICMP' : 'TCP' }}</option></select></label>
        <label>时间范围<select v-model="range" aria-label="时间范围" @change="changed()"><option v-for="r in ranges" :key="r" :value="r">{{ r }}</option><option value="peak">最近完整晚高峰 · 19:00—23:00</option><option value="custom">自定义</option></select></label>
        <label>显示时区<input v-model="zone" list="quality-zones" aria-label="显示时区" @change="changed()"><datalist id="quality-zones"><option value="Asia/Shanghai"/><option value="UTC"/><option value="Asia/Tokyo"/><option value="Europe/London"/><option value="America/New_York"/></datalist></label>
        <button class="primary-button" type="submit" :disabled="busy || !selected.length">{{ busy ? '正在比较…' : '比较' }}</button>
        <template v-if="range === 'custom'"><label>起点（所选时区）<input v-model="customStart" type="datetime-local" @change="changed()"></label><label>终点（所选时区）<input v-model="customEnd" type="datetime-local" @change="changed()"></label></template>
      </form>
      <button v-if="nextCursor && !targetsDirty" class="soft-button" type="button" :disabled="moreBusy || busy" @click="moreTargets">{{ moreBusy ? '加载中…' : '加载更多目标' }}</button>
      <p v-if="dirty && !busy" class="quality-hint" role="status">选择已变更，点击“比较”更新结果与可用目标。</p>
      <p v-if="error" class="quality-error" role="alert">{{ error }}</p><p v-if="notice" class="quality-hint" role="status">{{ notice }}</p>
      <p v-if="busy" class="quality-hint" role="status" aria-live="polite">正在读取所选节点的网络质量…</p>
      <template v-if="result">
        <header class="quality-result-heading"><h3>观测节点 → {{ targetLabel }}</h3><p>{{ textTime(result.requested_start) }} — {{ textTime(result.requested_end) }} · {{ result.timezone }}</p><p>每 {{ number(result.bucket_seconds / 60) }} 分钟一个统计点 · 地址族未上报 · 精确丢包率不可用</p></header>
        <div class="quality-legend" aria-label="图表节点图例"><button v-for="(n, i) in result.nodes" :key="n.node_id" type="button" :title="n.name" :aria-pressed="!hidden.includes(n.node_id)" @click="toggle(n.node_id)"><i :style="{ background: colors[i] }"></i>{{ i + 1 }}. {{ n.name }}</button></div>
        <p v-if="!hasRTT" class="quality-hint">所选范围没有可信的成功 RTT 样本；空白不代表 0 ms。</p>
        <div ref="chartElement" class="quality-chart" role="img" aria-label="所选节点的平均 RTT、执行失败率和覆盖率时序图"></div>
        <p class="quality-caption">淡红统计时段含至少一次执行失败，不代表整段持续故障。曲线空白表示该点没有可用值；覆盖率与执行失败率分开计算。</p>
        <div class="quality-table-wrap" tabindex="0" aria-label="范围汇总表，可横向滚动"><table class="quality-table"><caption>当前范围汇总 · RTT / 抖动单位 ms</caption><thead><tr><th>观测节点</th><th>覆盖率</th><th>执行失败率</th><th>P50</th><th>P95</th><th>抖动</th><th>成功 / 失败 / 缺测</th></tr></thead><tbody><tr v-for="(n, i) in result.nodes" :key="n.node_id"><th scope="row"><i :style="{ background: colors[i] }"></i>{{ i + 1 }}. {{ n.name }}</th><td>{{ number(n.coverage_pct) }}{{ n.coverage_pct === null ? '' : '%' }}</td><td>{{ number(n.execution_failure_pct) }}{{ n.execution_failure_pct === null ? '' : '%' }}</td><td :title="bounds(n.rtt_p50_bounds)">{{ percentile(n.rtt_p50_ms, n.quantile_method) }}</td><td :title="bounds(n.rtt_p95_bounds)">{{ percentile(n.rtt_p95_ms, n.quantile_method) }}</td><td :title="`${number(n.jitter_pairs, 0)} 对计划相邻成功样本`">{{ number(n.jitter_ms) }}</td><td>{{ number(n.counts.success, 0) }} / {{ number(n.counts.failure, 0) }} / {{ number(n.counts.missing, 0) }}</td></tr></tbody></table></div>
        <details class="quality-method"><summary>统计口径与实际可用范围</summary><p>覆盖率 = 已执行 / 成熟计划；失败率 = 执行失败 / 已执行。未执行与无回报属于缺测；零分母、未知历史和无样本均显示“—”。≈ 为汇总分位数区间中点，可在表格数值提示中查看上下界。抖动仅连接同一计划中相邻成功的真实 RTT，图表分桶不改变范围抖动。</p><ul><li v-for="n in result.nodes" :key="n.node_id">{{ n.name }}：{{ n.availability === 'no_plan_history' ? '没有保留的计划历史' : `${textTime(n.effective_start)} — ${textTime(n.effective_end)}` }}；成熟计划 {{ number(n.counts.planned, 0) }}，待成熟 {{ number(n.counts.pending, 0) }}，回报未执行 {{ number(n.counts.not_executed, 0) }}，无可信结果任务 {{ number(n.counts.unobserved_tasks, 0) }}。</li></ul><p>观测方向为节点到目标。Agent 没有上报地址族及已发/已收包数，因此不会推算 IPv4/IPv6 或精确 ICMP 丢包率。时刻与时区以本次查询结果为准。</p></details>
      </template>
    </section>
  </DsDialog>
</template>

<style scoped>
.network-quality { min-width: 0; color: var(--text); }
.quality-observers { min-width: 0; margin: 0 0 16px; padding: 12px; border: 1px solid var(--border); border-radius: 12px; }
.quality-observers legend { padding-inline: 6px; font-size: 13px; font-weight: 650; }
.quality-search { box-sizing: border-box; width: 100%; min-height: 36px; margin-bottom: 10px; padding: 7px 10px; border: 1px solid var(--border); border-radius: 8px; color: var(--text); background: var(--surface-secondary); font: inherit; font-size: 12px; }
.quality-node-options { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-bottom: 10px; max-height: 120px; overflow: auto; }
.quality-node-options label { display: flex; align-items: center; gap: 6px; max-width: 100%; min-height: 30px; font-size: 13px; }
.quality-node-options span { overflow-wrap: anywhere; }
.quality-node-options input { width: 16px; height: 16px; accent-color: var(--blue); }
.quality-controls { display: grid; grid-template-columns: minmax(180px, 1.5fr) minmax(160px, 1fr) minmax(150px, 1fr) auto; gap: 12px; align-items: end; margin-bottom: 12px; }
.quality-controls label { display: grid; gap: 6px; min-width: 0; color: var(--text-secondary); font-size: 12px; }
.quality-controls input, .quality-controls select { box-sizing: border-box; width: 100%; min-width: 0; min-height: 40px; padding: 8px 10px; color: var(--text); background: var(--surface-secondary); border: 1px solid var(--border); border-radius: 8px; font: inherit; }
.quality-controls button { min-height: 40px; }
.quality-hint, .quality-caption, .quality-result-heading p { color: var(--text-secondary); font-size: 12px; line-height: 1.6; }
.quality-error { padding: 10px; color: var(--danger, #b42318); background: color-mix(in srgb, #d33 8%, transparent); border-radius: 8px; font-size: 13px; }
.quality-result-heading { margin: 20px 0 12px; }
.quality-result-heading h3 { margin: 0 0 6px; font-size: 16px; overflow-wrap: anywhere; }
.quality-result-heading p { margin: 3px 0; }
.quality-legend { display: flex; flex-wrap: wrap; gap: 6px; }
.quality-legend button { display: flex; align-items: center; gap: 5px; max-width: 220px; padding: 6px 8px; border: 1px solid var(--border); border-radius: 7px; color: var(--text); background: var(--surface-secondary); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer; }
.quality-legend button[aria-pressed=false] { opacity: .45; }
.quality-legend i, .quality-table i { display: inline-block; width: 8px; height: 8px; flex: 0 0 auto; border-radius: 50%; margin-right: 4px; }
.quality-chart { height: 550px; width: 100%; margin-top: 14px; }
.quality-table-wrap { max-width: 100%; overflow: auto; border: 1px solid var(--border); border-radius: 10px; }
.quality-table { width: 100%; min-width: 720px; border-collapse: collapse; font-size: 12px; text-align: right; }
.quality-table caption { padding: 12px; text-align: left; color: var(--text-secondary); }
.quality-table th, .quality-table td { padding: 10px 12px; border-top: 1px solid var(--border); white-space: nowrap; }
.quality-table th:first-child { position: sticky; left: 0; z-index: 1; text-align: left; max-width: 220px; overflow: hidden; text-overflow: ellipsis; background: var(--surface-solid); }
.quality-method { margin-top: 16px; color: var(--text-secondary); font-size: 12px; line-height: 1.8; }
.quality-method summary { cursor: pointer; color: var(--text); font-weight: 600; }
.quality-method li { overflow-wrap: anywhere; }
@media(max-width: 760px) { .quality-controls { grid-template-columns: 1fr 1fr; }.quality-legend button { max-width: 160px; }.quality-table th:first-child { max-width: 150px; } }
@media(max-width: 420px) { .quality-controls { grid-template-columns: 1fr; }.quality-node-options { gap: 4px 8px; max-height: 184px; }.quality-table th:first-child { max-width: 120px; }.quality-chart { height: 550px; } }
</style>
