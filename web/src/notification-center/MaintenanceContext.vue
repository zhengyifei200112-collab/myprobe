<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { loadDeliveryMaintenance, loadIncidentMaintenance } from '../admin-api'
import type { DeliveryMaintenancePage, IncidentMaintenancePage, MaintenanceWindowRef } from '../admin-api'
import { DsButton } from '../design-system'
const props = defineProps<{ subject: 'incident' | 'delivery'; id: string }>()
const emit = defineEmits<{ maintenance: [] }>()
type Window = MaintenanceWindowRef & { active?: boolean; decisions?: IncidentMaintenancePage['windows'][number]['decisions']; recorded_at?: string }
const windows = ref<Window[]>([]), current = ref<IncidentMaintenancePage | null>(null), cursor = ref('')
const busy = ref(false), loaded = ref(false), error = ref('')
const labels = { observation: '维护期间评估事件', observation_gap: '观测间隔涉及维护', delivery: '队列通知抑制' }
const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
function date(value: string, timezone = browserZone) {
  try { return new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: timezone }) }
  catch { return value }
}
let generation = 0
async function refresh(more = false) {
  if (more && busy.value) return
  const token = ++generation
  busy.value = true; error.value = ''
  if (!more) { windows.value = []; current.value = null; loaded.value = false; cursor.value = '' }
  try {
    const page: IncidentMaintenancePage | DeliveryMaintenancePage = props.subject === 'incident' ? await loadIncidentMaintenance(props.id, more ? cursor.value : '') : await loadDeliveryMaintenance(props.id, more ? cursor.value : '')
    if (token !== generation) return
    if ('node_exists' in page) current.value = page
    windows.value = more ? [...new Map([...windows.value, ...page.windows].map(w => [w.id, w])).values()] : page.windows
    cursor.value = page.next_cursor; loaded.value = true
  } catch (e) { if (token === generation) error.value = e instanceof Error ? e.message : '维护关联加载失败，请重试。' }
  finally { if (token === generation) busy.value = false }
}
watch(() => [props.subject, props.id], () => { void refresh() }, { immediate: true })
onUnmounted(() => { generation++ })
</script>

<template>
  <section class="maintenance-context" :aria-label="subject==='incident' ? '事件维护关联' : '通知维护关联'">
    <header><h4>{{ subject==='incident' ? '节点维护状态与关联记录' : '该通知的维护关联' }}</h4><DsButton size="small" :disabled="busy" @click="refresh()">刷新维护关联</DsButton></header>
    <p v-if="busy" role="status">正在加载维护关联…</p><p v-if="error" role="alert">{{ error }}</p>
    <template v-if="current">
      <p v-if="!current.node_exists">节点已删除，保留历史维护记录。</p>
      <p v-else-if="current.node_in_maintenance">查询时节点处于维护中，通知受到抑制；故障状态继续记录。</p>
      <p v-else>查询时节点没有生效的维护窗口。</p>
      <small>状态核对时间：{{ date(current.checked_at) }}。点击刷新可核对最新状态。</small>
    </template>
    <p v-if="loaded && !windows.length">没有已记录的维护关联。旧历史不自动补录，没有记录不能证明未发生过抑制。</p>
    <article v-for="window in windows" :key="window.id">
      <strong>{{ window.reason }}</strong><span v-if="window.active"> · 该页查询时生效</span>
      <p>{{ date(window.starts_at,window.timezone) }} → {{ date(window.ends_at,window.timezone) }}（{{ window.timezone }}）</p>
      <p v-if="window.canceled_at">取消时间：{{ date(window.canceled_at,window.timezone) }}（{{ window.timezone }}）</p>
      <p v-if="window.recorded_at">此通知的关联记录时间：{{ date(window.recorded_at) }}</p>
      <ul v-if="window.decisions?.length"><li v-for="decision in window.decisions" :key="decision.kind">{{ labels[decision.kind] }} · 首次 {{ date(decision.first_at) }} · 最近 {{ date(decision.last_at) }}</li></ul>
      <p v-else-if="subject==='incident'">当前范围包含此节点，尚未记录该事件的维护处理决策。</p>
      <small>窗口 ID：{{ window.id }}</small>
    </article>
    <p v-if="windows.length">记录时间按浏览器时区 {{ browserZone }} 显示，代表维护处理决策，不能作为故障实际开始、恢复或外部收件时间。</p>
    <DsButton v-if="cursor" size="small" :disabled="busy" @click="refresh(true)">加载更多关联窗口</DsButton>
    <DsButton size="small" @click="emit('maintenance')">打开维护窗口管理</DsButton>
  </section>
</template>

<style scoped>
.maintenance-context { display:grid; gap:10px; border:1px solid var(--border); border-radius:12px; padding:14px; min-width:0; }
.maintenance-context header { display:flex; align-items:center; flex-wrap:wrap; gap:10px; justify-content:space-between; }
.maintenance-context h4,.maintenance-context p { margin:0; }
.maintenance-context article { padding:12px; border:1px solid var(--border); border-radius:10px; display:grid; gap:8px; }
.maintenance-context ul { padding-left:20px; margin:0; }
.maintenance-context small,.maintenance-context p,.maintenance-context li { overflow-wrap:anywhere; }
</style>
