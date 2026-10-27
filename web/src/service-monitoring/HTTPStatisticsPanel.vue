<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { NodeMetadata } from '../types'
import { loadHTTPService, loadHTTPStatistics } from '../admin-api'
import type { HTTPStatisticsResponse } from '../admin-api'

const props = defineProps<{ serviceID: string; serviceName: string; nodes: NodeMetadata[] }>()
defineEmits<{ close: [] }>()
const nodeID = ref('')
const hours = ref(24)
const busy = ref(false)
const error = ref('')
const data = ref<HTTPStatisticsResponse | null>(null)
let active = true
onUnmounted(() => { active = false })
function date(value: string) { return new Date(value).toLocaleString() }
function rate(value: number | null, empty: string) { return value === null ? empty : `${(value * 100).toFixed(2)}%` }
function clear() { data.value = null; error.value = '' }
async function query() {
  if (busy.value || !nodeID.value) return
  busy.value = true; clear()
  const end = new Date()
  const start = new Date(end.getTime() - hours.value * 3600000)
  try {
    const response = await loadHTTPStatistics(props.serviceID, nodeID.value, start.toISOString(), end.toISOString())
    if (active) data.value = response
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '无法读取统计。' }
  finally { if (active) busy.value = false }
}
onMounted(async () => {
  busy.value = true
  try {
    const { service } = await loadHTTPService(props.serviceID)
    if (active) nodeID.value = service.node_ids.find(id => props.nodes.some(node => node.id === id)) || props.nodes[0]?.id || ''
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '无法读取服务配置。' }
  finally { if (active) busy.value = false }
})
</script>

<template>
  <section class="admin-panel http-statistics" aria-label="服务检测统计">
    <h2>{{ serviceName }} · 检测统计</h2>
    <p>按观测节点分别计算。成功率只计有效检查；覆盖率同时反映未执行、离线和未回报造成的缺测。</p>
    <form @submit.prevent="query">
      <fieldset :disabled="busy" class="statistics-controls">
        <label>统计观测节点<select v-model="nodeID" @change="clear"><option value="" disabled>请选择节点</option><option v-for="node in nodes" :key="node.id" :value="node.id">{{ node.name }}</option></select></label>
        <label>统计时间范围<select v-model.number="hours" @change="clear"><option :value="1">最近 1 小时</option><option :value="24">最近 24 小时</option><option :value="168">最近 7 天</option><option :value="720">最近 30 天</option></select></label>
        <button class="primary-button" :disabled="!nodeID">查询统计</button>
      </fieldset>
    </form>
    <p v-if="busy" role="status">正在读取…</p>
    <p v-if="error" class="form-message error" role="alert">{{ error }}</p>
    <template v-if="data">
      <p v-if="!data.maintenance_excluded" class="form-message">尚未排除维护窗口，以下数据不作为维护调整后的可用率或 SLA。</p>
      <p v-if="!data.execution_enabled">服务端自动探测未启用；以下仅反映已记录的历史。</p>
      <dl class="statistics-counts">
        <div><dt>有效检测成功率</dt><dd>{{ rate(data.statistics.success_rate, '无有效样本') }}</dd></div>
        <div><dt>有效检测覆盖率</dt><dd>{{ rate(data.statistics.coverage, '无计划样本') }}</dd></div>
        <div><dt>计划检查</dt><dd>{{ data.statistics.expected }}</dd></div>
        <div><dt>成功</dt><dd>{{ data.statistics.success }}</dd></div>
        <div><dt>失败</dt><dd>{{ data.statistics.failure }}</dd></div>
        <div><dt>缺测</dt><dd>{{ data.statistics.missing }}</dd></div>
        <div><dt>其中未执行有效检查</dt><dd>{{ data.statistics.unobserved }}</dd></div>
      </dl>
      <p v-if="data.statistics.start === data.statistics.end">所选范围没有可统计的已保留历史或成熟样本，不表示 100% 正常。</p>
      <dl class="statistics-ranges">
        <dt>请求范围</dt><dd>{{ date(data.statistics.requested_start) }} — {{ date(data.statistics.requested_end) }}</dd>
        <dt>实际统计范围</dt><dd>{{ date(data.statistics.start) }} — {{ date(data.statistics.end) }}</dd>
        <dt>历史保留起点</dt><dd>{{ date(data.statistics.retained_from) }}</dd>
        <dt>可确认计划起点</dt><dd>{{ date(data.statistics.schedule_known_from) }}</dd>
        <dt>服务端统计时间</dt><dd>{{ date(data.server_time) }}</dd>
      </dl>
      <p>时间按浏览器本地时区显示，范围包含开始、不包含结束。最近 126 秒暂不计入，以等待任务完成和回报；已清理或无法重建的历史也不计入。</p>
    </template>
    <button type="button" @click="$emit('close')">关闭统计</button>
  </section>
</template>

<style scoped>
.http-statistics { padding: 24px; min-width: 0; }
.http-statistics h2, .http-statistics p, .http-statistics dd { overflow-wrap: anywhere; }
.statistics-controls { border: 0; padding: 0; margin: 16px 0; min-width: 0; display: flex; flex-wrap: wrap; align-items: end; gap: 12px; }
.statistics-controls label { display: grid; gap: 8px; min-width: 0; flex: 1 1 200px; }
.statistics-counts { display: grid; grid-template-columns: repeat(auto-fit,minmax(150px,1fr)); gap: 16px; margin: 20px 0; }
.statistics-counts dd { font-size: 1.25rem; font-weight: 600; margin: 6px 0 0; }
.statistics-ranges dd { margin: 4px 0 14px; }
</style>
