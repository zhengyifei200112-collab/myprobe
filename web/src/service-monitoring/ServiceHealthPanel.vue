<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { loadHTTPServiceHealth } from '../admin-api'
import type { HTTPServiceHealthResponse, HTTPServiceSummary, HTTPObserverHealth } from '../admin-api'
import { healthIsFresh, healthLabel, observationReasons } from './health-display'
const props = defineProps<{ service: HTTPServiceSummary }>()
defineEmits<{ close: []; events: [] }>()
const data = ref<HTTPServiceHealthResponse | null>(null), busy = ref(false), error = ref('')
const clock = ref(performance.now()), requestedAt = ref(0)
const fresh = computed(() => !!data.value && healthIsFresh(data.value.health, clock.value - requestedAt.value))
let active = true
let timer: ReturnType<typeof setInterval> | undefined
function date(value: string) { const d = new Date(value); return Number.isFinite(d.getTime()) ? d.toLocaleString() : '未知时间' }
function observerLabel(o: HTTPObserverHealth) {
  if (!fresh.value) return '等待刷新状态'
  if (o.status === 'success') return '检查成功'
  if (o.status === 'failure') return observationReasons[o.error_class || ''] || '检查失败'
  return observationReasons[o.error_class || ''] || observationReasons[o.reason || ''] || '观测状态未知'
}
async function refresh() {
  if (busy.value) return
  busy.value = true; error.value = ''
  const startedAt = performance.now()
  try {
    const response = await loadHTTPServiceHealth(props.service.id)
    if (!active) return
    if (!response.health || !Array.isArray(response.observers) || response.service.id !== props.service.id) throw new Error('无法确认服务状态，请刷新重试。')
    data.value = response; requestedAt.value = startedAt; clock.value = performance.now()
  } catch (e) { if (active) { data.value = null; error.value = e instanceof Error ? e.message : '服务状态暂时不可用，请重试。' } }
  finally { if (active) busy.value = false }
}
onMounted(() => { refresh(); timer = setInterval(() => { clock.value = performance.now() }, 1000) })
onUnmounted(() => { active = false; if (timer) clearInterval(timer) })
</script>

<template>
  <section class="admin-panel service-health" aria-label="服务当前状态">
    <header><h2>{{ data?.service.name || service.name }} · 当前状态</h2><div class="form-actions"><button :disabled="busy" @click="refresh">刷新当前状态</button><button @click="$emit('events')">查看服务告警与事件</button><button @click="$emit('close')">关闭当前状态</button></div></header>
    <p>汇总规则：任一必选观测点失败即降级；没有失败但存在未知时显示未知；全部观测点都有新鲜成功结果才显示正常。</p>
    <p v-if="busy" role="status">正在读取当前状态…</p>
    <p v-if="error" role="alert">{{ error }} 当前状态未确认，不表示服务正常。</p>
    <template v-if="data">
      <p class="health-state" :data-state="fresh ? data.health.state : 'unknown'">{{ healthLabel(data.health, fresh) }}</p>
      <p>状态快照截至 {{ date(data.health.as_of) }} · 配置版本 {{ data.service.revision }}。新任务或连接变化请刷新核对。</p>
      <p v-if="!fresh" role="status">快照已过期，请刷新当前状态；此前结果不能确认现在正常。</p>
      <dl v-if="fresh" class="health-counts">
        <div><dt>当前有效观测</dt><dd>{{ data.health.success + data.health.failure }} / {{ data.health.required_observers }}</dd></div>
        <div><dt>检查成功</dt><dd>{{ data.health.success }}</dd></div><div><dt>检查失败</dt><dd>{{ data.health.failure }}</dd></div><div><dt>未知</dt><dd>{{ data.health.unknown }}</dd></div>
      </dl>
      <p>当前覆盖只表示有多少已选观测点有新鲜有效结果，不是时间范围内的成功率或 SLA。告警持续时间、静默和维护通知不会把失败改成成功。</p>
      <p v-if="!data.observers.length">未分配观测节点，无法确认服务状态；当前覆盖无分母。</p>
      <ul v-else class="observer-list"><li v-for="o in data.observers" :key="o.node_id" :data-state="fresh ? o.status : 'unknown'">
        <strong>{{ o.node_name }}</strong><p>{{ observerLabel(o) }}</p>
        <p v-if="fresh && o.status !== 'unknown'">{{ o.status_code ? `HTTP ${o.status_code} · ` : '' }}{{ o.duration_ms === undefined ? '' : `耗时 ${o.duration_ms.toFixed(1)} ms` }}</p>
        <p v-if="o.scheduled_at">最近任务计划 {{ date(o.scheduled_at) }}</p><p v-if="o.completed_at">最近结果完成 {{ date(o.completed_at) }}</p>
      </li></ul>
    </template>
  </section>
</template>

<style scoped>
.service-health{display:grid;gap:18px;padding:24px;overflow-wrap:anywhere}.service-health header{display:grid;gap:16px}.service-health h2,.service-health p{margin:0}.service-health h2{font-size:22px}.service-health p{line-height:1.6}.service-health .form-actions{flex-wrap:wrap}.health-state{font-weight:750;color:var(--muted)}.health-state[data-state=healthy]{color:var(--green)}.health-state[data-state=degraded]{color:var(--red)}.health-counts{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin:0}.health-counts div{padding:16px;border:1px solid var(--border);border-radius:12px;min-width:0}.health-counts dt{font-size:12px;color:var(--muted)}.health-counts dd{font-size:22px;font-weight:750;margin:8px 0 0}.observer-list{display:grid;gap:12px;list-style:none;margin:0;padding:0}.observer-list li{display:grid;gap:8px;padding:16px;border:1px solid var(--border);border-radius:12px}.observer-list li[data-state=failure]{border-color:color-mix(in srgb,var(--red) 45%,var(--border))}@media(max-width:600px){.service-health{padding:16px}.service-health h2{font-size:20px}.health-counts{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
