<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { loadNotificationAggregate, loadNotificationAggregates, notificationReadError, type AggregateContext } from '../admin-api'
import { notificationPath } from '../notification-links'
import { DsButton, DsEmptyState } from '../design-system'

const props = defineProps<{ initialAggregateId?: string; initialSnapshot?: string }>()
const items = ref<Awaited<ReturnType<typeof loadNotificationAggregates>>['aggregates']>([])
const nextBefore = ref(0), loaded = ref(false), busy = ref(false), error = ref('')
const selectedID = ref(props.initialAggregateId || ''), snapshot = ref(props.initialSnapshot || '')
const detail = ref<AggregateContext | null>(null), detailBusy = ref(false), detailError = ref('')
const heading = ref<HTMLElement | null>(null)
let active = true, generation = 0
onUnmounted(() => { active = false; generation++ })
const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false })
const labels: Record<string, string> = { pending: '等待发送', inflight: '发送中', finished: '处理结束', delivered: '渠道已确认成功', failed: '发送失败', canceled: '已取消', prepared: '已准备，尚未授权发送', started: '已开始发送', unknown: '结果未知，可能已送达' }
const visibleAttempts = computed(() => detail.value?.attempts.filter(a => !snapshot.value || a.content?.snapshot_id === snapshot.value) || [])
const eventPath = (id: string) => notificationPath({ kind: 'incident', id })
async function refresh(more = false) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const page = await loadNotificationAggregates(more ? nextBefore.value : 0)
    if (!active) return
    items.value = more ? [...items.value, ...page.aggregates.filter(g => !items.value.some(old => old.id === g.id))] : page.aggregates
    nextBefore.value = page.next_before; loaded.value = true
  } catch (e) { if (active) error.value = notificationReadError(e, '聚合列表') }
  finally { if (active) busy.value = false }
}
async function inspect(id: string, more = false, fromList = false) {
  const current = ++generation
  if (fromList) snapshot.value = ''
  selectedID.value = id; detailBusy.value = true; detailError.value = ''
  if (!more) detail.value = null
  await nextTick(); if (!more) heading.value?.focus()
  try {
    let page = await loadNotificationAggregate(id, more ? detail.value?.next_before || 0 : 0)
    if (!active || current !== generation) return
    let attempts = more ? [...(detail.value?.attempts || []), ...page.attempts] : page.attempts
    // A deep link names immutable content across retries. Search bounded pages,
    // and leave a user action when older history remains; never show a different
    // version as the requested one.
    for (let n = 0; n < 3 && snapshot.value && !attempts.some(a => a.content?.snapshot_id === snapshot.value) && page.next_before; n++) {
      const older = await loadNotificationAggregate(id, page.next_before)
      if (!active || current !== generation) return
      attempts = [...attempts, ...older.attempts]; page = older
    }
    detail.value = { ...page, attempts }
  } catch (e) { if (active && current === generation) detailError.value = notificationReadError(e, '聚合记录') }
  finally { if (active && current === generation) detailBusy.value = false }
}
onMounted(() => { void refresh(); if (selectedID.value) void inspect(selectedID.value) })
</script>

<template>
  <section class="aggregate-manager" aria-label="通知聚合记录">
    <header><div><h2>通知聚合</h2><p>每组最多 10 个事件，窗口不会因后续事件延长。当前任务结果与历史发送内容分别展示。</p></div><DsButton :disabled="busy" @click="refresh()">刷新聚合列表</DsButton></header>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="busy" role="status">正在加载聚合列表…</p>
    <article v-for="g in items" :key="g.id">
      <h3>{{ g.channel_name || '原通知通道' }} · {{ g.notification_type === 'firing' ? '故障汇总' : '恢复汇总' }}</h3>
      <p>{{ labels[g.status] || g.status }} · 窗口 {{ date(g.starts_at) }} → {{ date(g.ends_at) }}</p>
      <p>等待 {{ g.totals.pending }} · 发送中 {{ g.totals.inflight }} · 成功 {{ g.totals.delivered }} · 失败 {{ g.totals.failed }} · 取消 {{ g.totals.canceled }}</p>
      <DsButton size="small" @click="inspect(g.id, false, true)">查看聚合详情</DsButton>
    </article>
    <DsEmptyState v-if="loaded && !busy && !items.length" title="暂无聚合记录" description="在范围策略中设置聚合等待，触发事件后会记录在这里。" />
    <DsButton v-if="nextBefore" :disabled="busy" @click="refresh(true)">加载更多聚合记录</DsButton>
    <section v-if="selectedID" class="aggregate-detail" aria-label="聚合详情">
      <h3 ref="heading" tabindex="-1">聚合详情</h3>
      <DsButton :disabled="detailBusy" @click="inspect(selectedID)">刷新聚合详情</DsButton>
      <p v-if="detailBusy" role="status">正在加载聚合详情…</p><p v-if="detailError" role="alert">{{ detailError }}</p>
      <template v-if="detail">
        <p>{{ labels[detail.aggregate.status] || detail.aggregate.status }} · {{ detail.aggregate.notification_type === 'firing' ? '故障汇总' : '恢复汇总' }} · 策略版本 {{ detail.aggregate.policy_revision }}</p>
        <p>聚合窗口：{{ date(detail.aggregate.starts_at) }} → {{ date(detail.aggregate.ends_at) }}。当前结果检查于 {{ date(detail.checked_at) }}。</p>
        <p>“处理结束”表示没有待发送成员，不代表所有事件都发送成功。渠道成功确认也不代表收件人已经阅读。</p>
        <h4>当前逐事件结果</h4>
        <ul class="member-list"><li v-for="m in detail.members" :key="m.id"><a :href="eventPath(m.incident_id)">{{ m.node_name }} · 查看事件</a><p>{{ labels[m.status] || m.status }} · {{ m.incident_state === 'firing' ? '故障持续' : m.incident_state === 'resolved' ? '事件已恢复或结束' : '等待触发' }}{{ m.observation_stale ? ' · 观测已过期，等待新数据' : '' }}<span v-if="m.error_class"> · {{ m.error_class }}</span></p></li></ul>
        <h4>{{ snapshot ? '通知入口对应的内容及重试' : '每次发送尝试' }}</h4>
        <p>这里展示冻结的摘要与成员，渠道可能调整格式或进一步截短。内容已生成不代表已经发出或被接收；请结合每次尝试的结果判断。</p>
        <p v-if="snapshot">以下内容对应原通知中的摘要；相同内容的多次重试共用此入口。</p>
        <p v-if="snapshot && !visibleAttempts.length" role="status">{{ detail.next_before ? '尚未在已加载记录中找到原通知内容，请继续加载较早记录。' : '未找到原通知对应的内容，记录可能来自旧环境或已被清理。' }}</p>
        <DsButton v-if="snapshot" size="small" @click="snapshot = ''">查看此组全部尝试</DsButton>
        <p v-if="!snapshot && !detail.attempts.length">尚未开始发送尝试。</p>
        <article v-for="a in visibleAttempts" :key="a.number" class="attempt">
          <h5>第 {{ a.number }} 次 · {{ labels[a.outcome] || a.outcome }}</h5>
          <p>{{ date(a.started_at) }}<span v-if="a.completed_at"> → {{ date(a.completed_at) }}</span><span v-if="a.error_class"> · {{ a.error_class }}</span></p>
          <p v-if="a.outcome === 'unknown'">外部渠道可能已收到这次通知。重试可能重复通知；该记录不能作为确定未发送的证据。</p>
          <p v-if="a.outcome === 'failed' && ['aggregate_payload_invalid','aggregate_payload_too_large'].includes(a.error_class || '')">未发起外部发送：聚合正文无效或超限。请检查配置和数据；本次任务不会自动重试。</p>
          <p v-if="a.outcome === 'canceled'">本次未获发送授权，关联成员没有继承外部发送结果。</p>
          <template v-if="a.content"><h6>{{ a.content.title }}</h6><pre>{{ a.content.message }}</pre><p>下列成员及观测时间来自这次冻结的发送内容：</p><ul><li v-for="m in a.content.members" :key="m.incident_id"><a :href="eventPath(m.incident_id)">{{ m.node_name }} · 查看事件</a> · 观测 {{ date(m.observed_at) }}</li></ul></template>
          <p v-else>{{ a.outcome === 'prepared' ? '尚未冻结发送内容；准备记录不表示已发送。' : '本次未生成可发送正文。' }}</p>
          <details><summary>本次成员关联</summary><ul><li v-for="m in a.members" :key="m.delivery_id">{{ m.node_name }} · 逐事件第 {{ m.delivery_attempt }} 次 · {{ m.included ? '已纳入冻结的发送内容' : '未纳入本次发送内容' }}</li></ul></details>
        </article>
        <DsButton v-if="detail.next_before" :disabled="detailBusy" @click="inspect(selectedID, true)">加载较早发送尝试</DsButton>
      </template>
    </section>
  </section>
</template>

<style scoped>
.aggregate-detail>p{margin:0}.aggregate-detail>button{justify-self:start}
.aggregate-manager{display:grid;gap:16px;min-width:0}.aggregate-manager header{display:flex;align-items:end;justify-content:space-between;gap:16px;flex-wrap:wrap}.aggregate-manager article,.aggregate-detail{padding:20px;border:1px solid var(--border);border-radius:16px;background:var(--surface);min-width:0;overflow-wrap:anywhere}.aggregate-manager h2,.aggregate-manager h3{margin:0 0 8px}.aggregate-manager p{color:var(--muted);line-height:1.6}.aggregate-detail{display:grid;gap:12px}.aggregate-detail h4{margin:12px 0 0}.attempt h5{font-size:15px;margin:0}.attempt h6{font-size:14px;margin:12px 0}.attempt pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.7;background:var(--surface-subtle);padding:12px;border-radius:10px}.aggregate-manager ul{padding-left:22px;line-height:1.7}.member-list li+li{margin-top:12px}.aggregate-manager a{color:var(--blue)}.aggregate-manager a:focus-visible,.aggregate-manager summary:focus-visible{outline:2px solid var(--blue);outline-offset:3px}.aggregate-manager summary{cursor:pointer}@media(max-width:560px){.aggregate-manager article,.aggregate-detail{padding:14px}}
</style>
