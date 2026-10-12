<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { AdminAPIError, cancelIncidentSilence, createIncidentSilence, loadIncidentSilences, type SilencePage } from '../admin-api'
import { DsButton, DsInput, DsTextarea } from '../design-system'
import { acknowledgementNoteBytes, normalizedAcknowledgementNote, validAcknowledgementNote } from './acknowledgement-recovery'
import { readSilenceRecovery, silenceRecoveryKey, type PendingSilence } from './silence-recovery'
import SilenceRecords from './SilenceRecords.vue'
const props = defineProps<{ incidentId: string; nodeName: string }>()
const emit = defineEmits<{ changed: [] }>()
const view = ref<SilencePage | null>(null), minutes = ref<string | number>('30'), reason = ref('')
const pending = ref<PendingSilence | null>(null), recoveryRaw = ref<string | null>(null), invalidRecovery = ref(false), reviewed = ref(false)
const loading = ref(false), busy = ref(false), error = ref(''), recoveryError = ref(''), notice = ref('')
const duration = computed(() => Number(minutes.value) * 60)
const validDuration = computed(() => String(minutes.value).trim() !== '' && Number.isInteger(Number(minutes.value)) && duration.value >= 60 && duration.value <= 604800)
const locked = computed(() => loading.value || busy.value || !!pending.value || invalidRecovery.value || !view.value)
const date = (v: string) => new Date(v).toLocaleString('zh-CN', { hour12: false })
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
let generation = 0
function describe(e: unknown) {
  if (e instanceof AdminAPIError) {
    const labels: Record<string,string> = { invalid_silence: '请检查期限和原因，期限为 1 分钟至 7 天，原因最多 1000 个 UTF-8 字节。', silence_active: '已有生效静默，请查看原记录，取消或到期后再设置。', silence_changed: '静默或事件已变化，请刷新记录后再操作。', incident_not_firing: '事件当前不是持续故障，不能新建静默。', silence_not_found: '事件或静默已不存在，请刷新记录。', silence_request_conflict: '原请求身份存在冲突，请核对静默及审计记录，保留原请求。' }
    return labels[e.code || ''] || e.message
  }
  return e instanceof Error ? e.message : '静默操作失败，请重试。'
}
function restore() {
  pending.value = null; recoveryRaw.value = null; invalidRecovery.value = false; recoveryError.value = ''
  try { const raw = sessionStorage.getItem(silenceRecoveryKey); recoveryRaw.value = raw; if (raw !== null) pending.value = readSilenceRecovery(raw) }
  catch { invalidRecovery.value = true; recoveryError.value = '无法读取待确认静默记录。请先核对静默与审计，再处理本地记录。' }
}
async function refresh(more = false) {
  const current = ++generation, id = props.incidentId, cursor = more ? view.value?.next_cursor || '' : ''
  loading.value = true; error.value = ''
  try { const page = await loadIncidentSilences(id, cursor); if (current === generation) view.value = more && view.value ? { ...page, silences: [...view.value.silences, ...page.silences] } : page }
  catch (e) { if (current === generation) { if (!more) view.value = null; error.value = describe(e) } }
  finally { if (current === generation) loading.value = false }
}
function removeOwn(raw: string) { if (sessionStorage.getItem(silenceRecoveryKey) === raw) sessionStorage.removeItem(silenceRecoveryKey); restore() }
function newOperation(operation: 'create' | 'cancel', acknowledge = false): PendingSilence {
  const base = { version: 1 as const, incidentID: props.incidentId, nodeName: props.nodeName, requestID: Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2,'0')).join('') }
  if (operation === 'cancel') {
    if (!view.value?.active_silence) throw new Error('请先刷新生效静默。')
    return { ...base, operation, silenceID: view.value.active_silence.id, revision: view.value.active_silence.revision }
  }
  if (view.value?.incident_state !== 'firing' || view.value.active_silence || !validDuration.value || !validAcknowledgementNote(reason.value)) throw new Error('请核对事件、期限和原因。')
  return { ...base, operation, params: { duration_seconds: duration.value, reason: normalizedAcknowledgementNote(reason.value), acknowledge } }
}
async function submit(kind: 'create' | 'cancel' = 'create', acknowledge = false) {
  if (busy.value || invalidRecovery.value || (!pending.value && locked.value)) return
  const current = generation; busy.value = true; error.value = ''; notice.value = ''
  let operation = pending.value, raw = recoveryRaw.value
  try {
    if (!operation) {
      if (sessionStorage.getItem(silenceRecoveryKey) !== null) { restore(); throw new Error('还有静默结果待确认，请先核对原操作。') }
      operation = newOperation(kind, acknowledge); raw = JSON.stringify(operation)
      sessionStorage.setItem(silenceRecoveryKey, raw); pending.value = operation; recoveryRaw.value = raw
    }
    if (!raw || sessionStorage.getItem(silenceRecoveryKey) !== raw) { restore(); throw new Error('待确认静默记录已变化，请重新核对。') }
    let result
    try { result = operation.operation === 'create' ? await createIncidentSilence(operation.incidentID,operation.requestID,operation.params) : await cancelIncidentSilence(operation.incidentID,operation.silenceID,operation.requestID,operation.revision) }
    catch (e) {
      if (e instanceof AdminAPIError && [400,404,409].includes(e.status) && e.code !== 'silence_request_conflict') { removeOwn(raw); emit('changed'); if (current === generation) { notice.value = describe(e); await refresh() }; return }
      throw e
    }
    removeOwn(raw); emit('changed')
    if (current === generation) {
      notice.value = operation.incidentID !== props.incidentId ? `已确认 ${operation.nodeName} 的静默结果；当前事件未被修改。` : result.replayed ? '已确认原静默操作，原期限保持不变。' : operation.operation === 'cancel' ? '静默已取消；仍受维护窗口约束，通知等待新鲜观测。' : result.acknowledgement_created ? '已记录知悉并设置静默；故障观测继续。' : operation.params.acknowledge ? '已设置静默，已有知悉记录保持不变；故障观测继续。' : '已设置静默；故障观测继续。'
      reason.value = ''; await refresh()
    }
  } catch (e) { if (current === generation) error.value = describe(e) }
  finally { busy.value = false }
}
function clearRecovery() {
  if (!reviewed.value || busy.value) return
  try { if (sessionStorage.getItem(silenceRecoveryKey) !== recoveryRaw.value) { restore(); reviewed.value = false; error.value = '本地记录已变化，请重新核对。'; return }; sessionStorage.removeItem(silenceRecoveryKey); restore(); reviewed.value = false; error.value = '' }
  catch { error.value = '无法清除本地记录，请检查浏览器存储权限。' }
}
watch(() => props.incidentId, () => { view.value = null; reason.value = ''; notice.value = ''; reviewed.value = false; restore(); void refresh() }, { immediate: true })
onUnmounted(() => { generation++ })
</script>
<template>
  <section class="incident-silence" aria-label="事件静默">
    <header><h4>限时静默</h4><DsButton size="small" :disabled="loading || busy" @click="refresh()">刷新静默记录</DsButton></header>
    <p>静默只暂停此故障事件的通知，继续采集和评估。维护或静默任一生效都会抑制通知；全部结束后等待新鲜观测。已发出的请求无法撤回。</p>
    <p v-if="loading" role="status">正在加载静默记录…</p><p v-if="error" role="alert">{{ error }}</p><p v-if="recoveryError" role="alert">{{ recoveryError }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="pending || invalidRecovery" class="silence-recovery" aria-label="待确认静默操作">
      <h5>上次静默结果待确认</h5><p>请重试原请求。刷新后可继续；关闭标签页可能丢失本地记录。数据库恢复后先核对备份时间与事件历史。</p>
      <p v-if="pending">{{ pending.operation === 'cancel' ? '取消静默' : '设置静默' }}：{{ pending.nodeName }}（事件 {{ pending.incidentID }}）</p>
      <p v-if="pending && pending.incidentID !== incidentId">这是另一事件的操作；重试只处理原事件。</p>
      <DsButton v-if="pending" :disabled="busy" @click="submit()">重试确认静默结果</DsButton>
      <details><summary>核对后处理本地静默记录</summary><p>清除不会撤销已保存的静默，也不能证明原请求失败。</p><label><input v-model="reviewed" type="checkbox" :disabled="busy">我已核对静默与审计记录</label><DsButton size="small" :disabled="!reviewed || busy" @click="clearRecovery">清除本地待确认静默记录</DsButton></details>
    </section>
    <template v-if="view">
      <p>核对时间：{{ date(view.checked_at) }}（{{ zone }}）。到期后刷新可查看当前状态。</p>
      <template v-if="view.active_silence"><p>此事件静默至 {{ date(view.active_silence.ends_at) }}（{{ zone }}），由 {{ view.active_silence.creator_name }} 设置。</p><DsButton :disabled="locked" @click="submit('cancel')">取消此事件静默</DsButton></template>
      <form v-else-if="view.incident_state === 'firing'" @submit.prevent="submit()">
        <DsInput v-model="minutes" label="静默时长（分钟）" type="number" min="1" max="10080" step="1" :disabled="locked" help="1 分钟至 7 天；按服务端时间开始。" />
        <DsTextarea v-model="reason" label="静默原因（仅管理端可见，可留空）" :disabled="locked" :rows="3" help="最多 1000 个 UTF-8 字节；中文通常每字 3 字节。" /><p>{{ acknowledgementNoteBytes(reason) }} / 1000 字节</p>
        <p>“知悉并静默”会在没有知悉记录时保存原因作为备注；已有首次记录继续保留。</p>
        <div class="actions"><DsButton type="submit" variant="primary" :disabled="locked || !validDuration || !validAcknowledgementNote(reason)">仅设置静默</DsButton><DsButton :disabled="locked || !validDuration || !validAcknowledgementNote(reason)" @click="submit('create',true)">知悉并静默</DsButton></div>
      </form>
      <p v-else>事件已恢复或结束，不能新建静默；历史继续保留。</p>
      <details><summary>此事件的静默历史（{{ view.silences.length }} 条已加载）</summary><SilenceRecords :records="view.silences" /><p v-if="!view.silences.length">暂无静默历史。</p><DsButton v-if="view.next_cursor" :disabled="loading || busy" @click="refresh(true)">加载更多静默历史</DsButton></details>
    </template>
  </section>
</template>
<style scoped>
.incident-silence{padding:16px;border:1px solid var(--border);border-radius:12px;display:grid;gap:10px;overflow-wrap:anywhere}.incident-silence header,.actions{display:flex;gap:12px;flex-wrap:wrap;align-items:center;justify-content:space-between}.incident-silence h4,.incident-silence h5,.incident-silence p{margin:0;line-height:1.6}.incident-silence form,.silence-recovery,.silence-recovery details{display:grid;gap:10px}.silence-recovery{padding:12px;border:1px solid var(--border);border-radius:10px}.silence-recovery label{display:flex;align-items:start;gap:8px}@media(max-width:560px){.incident-silence{padding:12px}}
</style>
