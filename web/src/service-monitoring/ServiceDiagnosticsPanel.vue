<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import type { NodeMetadata } from '../types'
import { loadHTTPDiagnostics, loadHTTPServiceHealth, type HTTPDiagnosticsResponse, type HTTPDiagnosticObservation } from '../admin-api'
import { notificationPath, parseNotificationTarget, type ServiceDiagnosticTarget } from '../notification-links'

const props = defineProps<{ serviceID: string; serviceName?: string; nodes: NodeMetadata[]; initialTarget?: ServiceDiagnosticTarget }>()
const emit = defineEmits<{ close: []; context: [target: ServiceDiagnosticTarget] }>()
const nodeID = ref(props.initialTarget?.observer || props.nodes[0]?.id || '')
const end = ref(props.initialTarget?.end || new Date().toISOString())
const start = ref(props.initialTarget?.start || new Date(Date.now() - 3600000).toISOString())
const busy = ref(false), error = ref(''), data = ref<HTTPDiagnosticsResponse | null>(null)
const heading = ref<HTMLElement | null>(null)
let active = true
onUnmounted(() => { active = false })
const statusLabels = { success: '检查成功', failure: '检查失败', unobserved: '未执行有效检查', pending: '等待回报', missing: '到期未回报' }
const evidence: Record<string, string> = {
  dns: 'DNS · 解析失败', refused: 'TCP · 连接被拒绝', timeout: '超时 · 具体阶段未记录', tls_invalid: 'TLS · 验证失败', certificate_expired: 'TLS · 证书已过期',
  status_mismatch: 'HTTP · 状态码不符合要求', content_mismatch: '内容断言 · 不符合要求', response_too_large: '响应限制 · 超过大小上限', redirect_limit: '重定向 · 超过次数上限',
  unsupported: '能力不支持', busy: 'Agent 繁忙', cancelled: '任务已取消', invalid_task: '任务无效', policy_denied: 'Agent 本地策略拒绝', internal: '执行器内部错误'
}
function date(value: string) { return new Date(value).toLocaleString() }
function explanation(item: HTTPDiagnosticObservation) {
  return item.error_class ? evidence[item.error_class] || '未记录可用分类' : item.status === 'missing' ? '收件宽限结束，未收到有效结果' : item.status === 'pending' ? '收件宽限尚未结束' : '已记录成功响应'
}
function clear() { data.value = null; error.value = '' }
async function query(after = '') {
  if (busy.value) return
  const target: ServiceDiagnosticTarget = { kind: 'service', id: props.serviceID, observer: nodeID.value, start: start.value, end: end.value }
  if (!parseNotificationTarget(notificationPath(target).slice('/admin'.length)).target) { clear(); error.value = '请选择观测节点，输入 UTC 时间，范围最多 31 天。'; return }
  busy.value = true; clear()
  try {
    const response = await loadHTTPDiagnostics(props.serviceID, nodeID.value, start.value, end.value, after)
    if (!active) return
    data.value = response; history.replaceState(null, '', notificationPath(target)); emit('context', target)
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '无法读取诊断，请重试。' }
  finally { if (active) busy.value = false }
}
onMounted(async () => {
  await nextTick(); if (active) heading.value?.focus()
  if (props.initialTarget) { query(); return }
  busy.value = true
  try {
    const response = await loadHTTPServiceHealth(props.serviceID)
    if (active) nodeID.value = response.observers[0]?.node_id || nodeID.value
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '无法读取分配节点，请手动选择。' }
  finally { if (active) busy.value = false }
})
</script>

<template>
  <section class="admin-panel service-diagnostics" aria-label="服务诊断">
    <h2 ref="heading" tabindex="-1">{{ data?.service.name || serviceName || '指定服务' }} · 历史诊断</h2>
    <p>查看已有检查证据；成功或失败与缺测分别计算。历史结果不表示当前健康状态。</p>
    <form @submit.prevent="query()">
      <fieldset :disabled="busy" class="diagnostic-controls">
        <label>诊断观测节点<select v-model="nodeID" aria-label="诊断观测节点" @change="clear"><option value="" disabled>请选择节点</option><option v-if="nodeID && !nodes.some(node => node.id === nodeID)" :value="nodeID">指定历史节点</option><option v-for="node in nodes" :key="node.id" :value="node.id">{{ node.name }}</option></select></label>
        <label>诊断开始（UTC）<input v-model="start" required placeholder="2026-01-01T00:00:00Z" @input="clear"></label>
        <label>诊断结束（UTC）<input v-model="end" required placeholder="2026-01-01T01:00:00Z" @input="clear"></label>
        <button class="primary-button" :disabled="!nodeID">查询诊断</button>
      </fieldset>
    </form>
    <p v-if="busy" role="status">正在读取诊断…</p>
    <p v-if="error" class="form-message error" role="alert">{{ error }}</p>
    <template v-if="data">
      <p>观测节点：{{ data.observer.name }}。本页读取时间：{{ date(data.server_time) }}。</p>
      <p v-if="!data.observer.currently_assigned" class="form-message">该节点当前未分配给此服务；以下保留已有历史。</p>
      <p v-if="!data.execution_enabled" class="form-message">服务端自动探测未启用；以下仅反映历史记录。</p>
      <dl class="diagnostic-counts"><div><dt>成熟计划槽位</dt><dd>{{ data.statistics.expected }}</dd></div><div><dt>有效成功</dt><dd>{{ data.statistics.success }}</dd></div><div><dt>有效失败</dt><dd>{{ data.statistics.failure }}</dd></div><div><dt>缺测（含未执行）</dt><dd>{{ data.statistics.missing }}</dd></div><div><dt>其中已报告未执行</dt><dd>{{ data.statistics.unobserved }}</dd></div></dl>
      <p class="form-message">统计使用原始计划槽位，尚未排除维护窗口；仅计成熟、已知且仍保留的历史，最近 126 秒等待任务与收件宽限，不进入分母。未下发任务不会出现在下方列表。</p>
      <p>请求：{{ date(data.statistics.requested_start) }} 至 {{ date(data.statistics.requested_end) }}；成熟统计：{{ date(data.statistics.start) }} 至 {{ date(data.statistics.end) }}。</p>
      <p>历史保留起点：{{ date(data.statistics.retained_from) }}；已知调度起点：{{ date(data.statistics.schedule_known_from) }}。</p>
      <p class="diagnostic-limit">当前只记录总耗时；DNS/TCP/TLS 分段耗时和解析地址尚未提供。超时不能定位具体阶段。</p>
      <p v-if="!data.observations.length">该范围没有保留的已下发任务；不能据此认定服务正常。</p>
      <ol v-else class="diagnostic-results"><li v-for="item in data.observations" :key="item.task_id" :data-status="item.status"><strong>{{ statusLabels[item.status] }}</strong><span>{{ explanation(item) }}</span><span>计划：{{ date(item.scheduled_at) }} · 配置版本 {{ item.revision }}{{ item.revision !== data.service.revision ? '（历史版本）' : '' }}</span><span v-if="item.completed_at">完成：{{ date(item.completed_at) }}</span><span v-if="item.status_code">状态码 {{ item.status_code }}</span><span v-if="item.duration_ms !== undefined">总耗时 {{ item.duration_ms.toFixed(1) }} ms</span><small>任务 {{ item.task_id }}</small></li></ol>
      <p v-if="data.next_cursor">每页最多 50 条。下一页会重新读取，期间回报和历史清理可能改变内容。</p>
      <button v-if="data.next_cursor" class="soft-button" :disabled="busy" @click="query(data.next_cursor)">诊断下一页</button>
    </template>
    <button class="soft-button" :disabled="busy" @click="emit('close')">关闭诊断</button>
  </section>
</template>

<style scoped>
.service-diagnostics { display: grid; gap: 18px; min-width: 0; padding: 24px; }
.service-diagnostics h2 { font-size: 22px; margin: 0; overflow-wrap: anywhere; }
.service-diagnostics p { margin: 0; line-height: 1.6; overflow-wrap: anywhere; }
.diagnostic-controls { border: 0; padding: 0; margin: 0; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.diagnostic-controls label { display: grid; gap: 6px; min-width: 0; }
.diagnostic-controls input, .diagnostic-controls select { width: 100%; min-width: 0; min-height: 40px; padding: 8px 10px; box-sizing: border-box; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
.diagnostic-controls button { justify-self: start; }
.diagnostic-counts { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 14px; margin: 0; }
.diagnostic-counts div { padding: 14px; border: 1px solid var(--border); border-radius: 12px; min-width: 0; }
.diagnostic-counts dt, .diagnostic-limit, .diagnostic-results small { color: var(--text-secondary); }
.diagnostic-counts dd { font-size: 22px; margin: 4px 0 0; }
.diagnostic-results { display: grid; gap: 12px; padding: 0; margin: 0; list-style: none; }
.diagnostic-results li { display: grid; gap: 5px; padding: 14px; border: 1px solid var(--border); border-radius: 12px; overflow-wrap: anywhere; }
.diagnostic-results [data-status="failure"] strong { color: var(--red); }
.diagnostic-results [data-status="failure"] { border-color: color-mix(in srgb, var(--red) 45%, var(--border)); }
@media (max-width: 767px) { .diagnostic-controls { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 600px) { .service-diagnostics { padding: 16px; } .service-diagnostics h2 { font-size: 20px; } .diagnostic-counts { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
