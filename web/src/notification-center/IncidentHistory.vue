<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { loadIncidents, loadIncidentDeliveries, loadDeliveryAttempts, type DeliveryAttempt, type Incident, type IncidentDelivery } from '../admin-api'
import { DsButton, DsSelect, DsEmptyState } from '../design-system'

const state = ref(''), items = ref<Incident[]>([]), next = ref(0)
const selected = ref<Incident | null>(null), deliveries = ref<IncidentDelivery[]>([]), deliveryNext = ref(0)
const busy = ref(false), detailBusy = ref(false), error = ref(''), detailError = ref('')
const detailHeading = ref<HTMLElement | null>(null)
const attempts = ref<Record<string, DeliveryAttempt[]>>({})
const attemptErrors = ref<Record<string, string>>({}), attemptBusy = ref<Record<string, boolean>>({})
async function showAttempts(id: string) {
  if (attemptBusy.value[id]) return
  attemptBusy.value[id] = true; attemptErrors.value[id] = ''
  try { attempts.value[id] = (await loadDeliveryAttempts(id)).attempts }
  catch (e) { attemptErrors.value[id] = e instanceof Error ? e.message : '尝试记录加载失败' }
  finally { attemptBusy.value[id] = false }
}
const outcomeLabels: Record<string, string> = { started: '正在尝试', delivered: '已送达', failed: '失败', unknown: '结果未知，可能已送达', canceled: '已取消' }
let detailGeneration = 0
const labels: Record<string, string> = { pending: '等待触发', firing: '故障持续', resolved: '已恢复或结束', inflight: '发送中', delivered: '已送达', failed: '发送失败', canceled: '已取消' }
const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false })
async function refresh(more = false) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await loadIncidents(state.value, more ? next.value : 0)
    items.value = more ? [...items.value, ...result.incidents] : result.incidents
    next.value = result.next_before
  } catch (e) { error.value = e instanceof Error ? e.message : '事件加载失败，请重试' }
  finally { busy.value = false }
}
async function inspect(item: Incident, more = false) {
  const generation = ++detailGeneration
  selected.value = item; detailBusy.value = true; detailError.value = ''
  if (!more) { deliveries.value = []; deliveryNext.value = 0 }
  if (!more) { await nextTick(); detailHeading.value?.focus() }
  try {
    const result = await loadIncidentDeliveries(item.id, more ? deliveryNext.value : 0)
    if (generation !== detailGeneration) return
    deliveries.value = more ? [...deliveries.value, ...result.deliveries] : result.deliveries
    deliveryNext.value = result.next_before
  } catch (e) { if (generation === detailGeneration) detailError.value = e instanceof Error ? e.message : '投递记录加载失败' }
  finally { if (generation === detailGeneration) detailBusy.value = false }
}
onMounted(() => refresh())
</script>

<template>
  <section class="incident-history" aria-label="故障事件">
    <header><div><h2>故障事件</h2><p>故障状态与通知是否送达分别记录。刷新可查看最新状态。</p></div><DsSelect v-model="state" label="事件状态" :disabled="busy" @update:model-value="refresh()"><option value="">全部状态</option><option value="pending">等待触发</option><option value="firing">故障持续</option><option value="resolved">已恢复或结束</option></DsSelect><DsButton :disabled="busy" @click="refresh()">刷新</DsButton></header>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="busy" role="status">正在加载事件…</p>
    <article v-for="item in items" :key="item.id"><h3>{{ item.node_name }} · {{ labels[item.state] }}</h3><p>{{ item.message }}</p><p v-if="item.observation_stale">数据已过期，等待新观测；当前状态不表示已经恢复。</p><p>开始：{{ date(item.started_at) }}{{ item.approximate_start ? '（旧记录迁移，时间近似）' : '' }}</p><p v-if="item.resolved_at">结束：{{ date(item.resolved_at) }}</p><DsButton size="small" @click="inspect(item)">查看 {{ item.node_name }} 的投递记录</DsButton></article>
    <DsEmptyState v-if="!busy && !error && !items.length" title="暂无匹配事件" description="告警满足触发条件后会记录在这里。" />
    <DsButton v-if="next" :disabled="busy" @click="refresh(true)">加载更多事件</DsButton>
    <section v-if="selected" class="delivery-detail" aria-label="事件投递记录" aria-live="polite"><h3 ref="detailHeading" tabindex="-1">{{ selected.node_name }} · 投递记录</h3><p v-if="detailError" role="alert">{{ detailError }}</p><DsButton :disabled="detailBusy" @click="inspect(selected)">刷新投递记录</DsButton><p v-if="detailBusy" role="status">正在加载投递记录…</p><article v-for="job in deliveries" :key="job.id"><strong>{{ job.channel_name }} · {{ job.status === 'pending' ? '等待发送' : labels[job.status] || job.status }}</strong><p>{{ job.notification_type === 'firing' ? '故障通知' : '恢复通知' }} · 已尝试 {{ job.attempt_count }} 次</p><p>{{ date(job.created_at) }}</p><p v-if="job.error_class">错误分类：{{ job.error_class }}</p><DsButton size="small" :disabled="attemptBusy[job.id]" @click="showAttempts(job.id)">查看每次尝试</DsButton><p v-if="attemptBusy[job.id]" role="status">正在加载尝试记录…</p><p v-if="attempts[job.id]?.length === 0 && !attemptBusy[job.id]">尚未开始投递尝试。</p><p v-if="attemptErrors[job.id]" role="alert">{{ attemptErrors[job.id] }}</p><ol v-if="attempts[job.id]"><li v-for="attempt in attempts[job.id]" :key="attempt.number">第 {{ attempt.number }} 次：{{ outcomeLabels[attempt.outcome] || attempt.outcome }} · {{ date(attempt.started_at) }}<span v-if="attempt.completed_at"> → {{ date(attempt.completed_at) }}</span><span v-if="attempt.error_class"> · {{ attempt.error_class }}</span></li></ol></article><p v-if="!detailBusy && !detailError && !deliveries.length">暂无投递任务；事件记录不依赖通知渠道可用性。</p><DsButton v-if="deliveryNext" :disabled="detailBusy" @click="inspect(selected, true)">加载更多投递记录</DsButton></section>
  </section>
</template>

<style scoped>
.incident-history{display:grid;gap:16px}.incident-history header{display:flex;gap:16px;align-items:end;flex-wrap:wrap}.incident-history header>div{flex:1;min-width:200px}.incident-history article,.delivery-detail{padding:20px;border:1px solid var(--border);border-radius:16px;background:var(--surface);overflow-wrap:anywhere}.incident-history h2,.incident-history h3{margin:0 0 8px}.incident-history p{color:var(--muted);line-height:1.6}.delivery-detail{display:grid;gap:12px}@media(max-width:560px){.incident-history header{align-items:stretch;flex-direction:column}.incident-history article,.delivery-detail{padding:14px}}
</style>
