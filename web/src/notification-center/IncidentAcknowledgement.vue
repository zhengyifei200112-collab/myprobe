<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { acknowledgeIncident, AdminAPIError, loadIncidentAcknowledgement, type IncidentAcknowledgementView } from '../admin-api'
import { DsButton, DsTextarea } from '../design-system'
import { acknowledgementNoteBytes, acknowledgementRecoveryKey, normalizedAcknowledgementNote, readAcknowledgementRecovery, validAcknowledgementNote, type PendingAcknowledgement } from './acknowledgement-recovery'

const props = defineProps<{ incidentId: string; nodeName: string; refreshVersion?: number; warning?: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const view = ref<IncidentAcknowledgementView | null>(null), note = ref('')
const pending = ref<PendingAcknowledgement | null>(null), recoveryRaw = ref<string | null>(null)
const recoveryInvalid = ref(false), reviewed = ref(false)
const loading = ref(false), busy = ref(false), error = ref(''), recoveryError = ref(''), notice = ref('')
const locked = computed(() => busy.value || loading.value || !!pending.value || recoveryInvalid.value || !view.value)
const bytes = computed(() => acknowledgementNoteBytes(note.value))
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const date = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false })
let generation = 0
function describe(e: unknown) {
  if (e instanceof AdminAPIError) {
    const messages: Record<string, string> = {
      invalid_acknowledgement: '请检查备注和请求内容。备注最多 1000 个 UTF-8 字节。',
      incident_already_acknowledged: '事件已被知悉；您的新备注未另行保存，请查看原记录。',
      incident_not_firing: props.warning ? '此提醒已结束，不能新建知悉记录。' : '事件当前不是持续故障，不能新建知悉记录。',
      incident_not_found: '事件已不存在，请刷新事件列表。',
      acknowledgement_request_conflict: '原请求身份存在冲突，请核对知悉记录和审计；不要建立新的请求。',
    }
    return messages[e.code || ''] || e.message
  }
  return e instanceof Error ? e.message : '操作失败，请重试。'
}
function restoreRecovery() {
  pending.value = null; recoveryRaw.value = null; recoveryInvalid.value = false; recoveryError.value = ''
  try {
    const raw = sessionStorage.getItem(acknowledgementRecoveryKey)
    recoveryRaw.value = raw
    if (raw !== null) pending.value = readAcknowledgementRecovery(raw)
  } catch {
    recoveryInvalid.value = true
    recoveryError.value = '无法读取待确认知悉记录。请先核对事件和审计记录，再处理本地记录。'
  }
}
async function refresh() {
  const current = ++generation, id = props.incidentId
  loading.value = true; error.value = ''
  try {
    const result = await loadIncidentAcknowledgement(id)
    if (current === generation) view.value = result
  } catch (e) { if (current === generation) { view.value = null; error.value = describe(e) } }
  finally { if (current === generation) loading.value = false }
}
function removeOwnRecovery(raw: string) {
  // An older response may not erase another operation saved in this tab.
  if (sessionStorage.getItem(acknowledgementRecoveryKey) === raw) sessionStorage.removeItem(acknowledgementRecoveryKey)
  restoreRecovery()
}
async function submit() {
  if (busy.value || recoveryInvalid.value) return
  const current = generation
  busy.value = true; error.value = ''; notice.value = ''
  let operation = pending.value, raw = recoveryRaw.value
  try {
    if (!operation) {
      if (loading.value || !view.value || view.value.acknowledgement || view.value.incident_state !== 'firing') return
      if (!validAcknowledgementNote(note.value)) throw new Error('备注最多 1000 个 UTF-8 字节，且不能包含控制字符。')
      // Read/write storage before posting; a write failure sends no request.
      if (sessionStorage.getItem(acknowledgementRecoveryKey) !== null) {
        restoreRecovery()
        throw new Error('还有一条知悉结果待确认，请先核对原操作。')
      }
      operation = { version: 1, incidentID: props.incidentId, nodeName: props.nodeName, requestID: Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join(''), note: normalizedAcknowledgementNote(note.value) }
      raw = JSON.stringify(operation)
      sessionStorage.setItem(acknowledgementRecoveryKey, raw)
      pending.value = operation; recoveryRaw.value = raw
    }
    if (!raw || sessionStorage.getItem(acknowledgementRecoveryKey) !== raw) {
      restoreRecovery()
      throw new Error('待确认记录已变化，请核对原操作。')
    }
    let result
    try {
      result = await acknowledgeIncident(operation.incidentID, operation.requestID, operation.note)
    } catch (e) {
      // 401/403/network/server errors and identity conflicts keep uncertainty.
      if (e instanceof AdminAPIError && [400,404,409].includes(e.status) && e.code !== 'acknowledgement_request_conflict') {
        removeOwnRecovery(raw)
        emit('changed')
        if (current === generation) { await refresh(); notice.value = describe(e) }
        return
      }
      throw e
    }
    removeOwnRecovery(raw)
    emit('changed')
    if (current === generation) {
      notice.value = operation.incidentID !== props.incidentId
        ? `已确认 ${operation.nodeName} 的知悉结果；本次操作未修改当前事件。`
        : props.warning ? '已记录或确认知悉；已有通知任务继续按规则处理，证书档位去重仍生效。' : result.replayed ? '已确认此前知悉结果，故障通知继续按规则发送。' : '已记录知悉，故障通知继续按规则发送。'
      note.value = ''
      await refresh()
    }
  } catch (e) {
    if (current === generation) error.value = describe(e)
  } finally { busy.value = false }
}
function clearRecovery() {
  if (!reviewed.value || busy.value) return
  try {
    // Only clear the record the operator has reviewed, not a later replacement.
    if (sessionStorage.getItem(acknowledgementRecoveryKey) !== recoveryRaw.value) {
      restoreRecovery(); reviewed.value = false
      error.value = '本地记录已变化，请重新核对。'; return
    }
    sessionStorage.removeItem(acknowledgementRecoveryKey)
    restoreRecovery(); reviewed.value = false; error.value = ''
  } catch { error.value = '无法清除本地记录，请检查浏览器存储权限。' }
}
watch(() => props.incidentId, () => {
  view.value = null; note.value = ''; error.value = ''; notice.value = ''; reviewed.value = false
  restoreRecovery(); void refresh()
}, { immediate: true })
onUnmounted(() => { generation++ })
watch(() => props.refreshVersion, () => { void refresh() })
</script>

<template>
  <section class="incident-ack" aria-label="事件知悉">
    <header><h4>知悉与处理记录</h4><DsButton size="small" :disabled="loading || busy" @click="refresh">刷新知悉记录</DsButton></header>
    <p>{{ warning ? '知悉表示有人处理此证书预警；不会暂停已有通知任务，也不证明证书已续期。' : '知悉表示有人处理，不会暂停通知，也不表示故障已经恢复。' }}</p>
    <p v-if="loading" role="status">正在加载知悉记录…</p><p v-if="error" role="alert">{{ error }}</p><p v-if="recoveryError" role="alert">{{ recoveryError }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="pending || recoveryInvalid" class="ack-recovery" aria-label="待确认知悉操作">
      <h5>上次知悉结果待确认</h5>
      <p>请保留原请求重试。当前标签页刷新后可继续；关闭标签页可能丢失记录。数据库恢复后先核对备份时间与事件记录。</p>
      <p v-if="pending">待确认：{{ pending.nodeName }}（事件 {{ pending.incidentID }}）</p>
      <p v-if="pending && pending.incidentID !== incidentId">这是另一事件的操作；确认结果后才能新建当前事件的知悉记录。</p>
      <DsButton v-if="pending" :disabled="busy" variant="primary" @click="submit">重试确认知悉结果</DsButton>
      <details><summary>核对后处理本地知悉记录</summary><p>清除不会撤销已保存的知悉，也不能证明原请求失败。</p><label><input v-model="reviewed" type="checkbox" :disabled="busy">我已核对事件知悉与审计记录</label><DsButton size="small" :disabled="!reviewed || busy" @click="clearRecovery">清除本地待确认知悉记录</DsButton></details>
    </section>
    <template v-if="view?.acknowledgement">
      <p v-if="view.incident_state==='resolved'">事件已恢复或结束，首次知悉记录继续保留。</p>
      <p>已知悉：{{ view.acknowledgement.actor_name }} · {{ date(view.acknowledgement.acknowledged_at) }}（{{ zone }}）</p>
      <p v-if="view.acknowledgement.note" class="ack-note">{{ view.acknowledgement.note }}</p><p v-else>未填写备注。</p>
      <p>首次知悉记录保留原操作者、时间和备注。</p>
    </template>
    <form v-else-if="view?.incident_state === 'firing'" @submit.prevent="submit">
      <DsTextarea v-model="note" label="知悉备注（仅管理端可见，可留空）" :disabled="locked" :rows="3" help="最多 1000 个 UTF-8 字节；中文通常每字 3 字节。" />
      <p>{{ bytes }} / 1000 字节</p>
      <DsButton type="submit" variant="primary" :disabled="locked || !validAcknowledgementNote(note)">{{ warning ? '知悉此证书提醒' : '知悉此故障（继续通知）' }}</DsButton>
    </form>
    <p v-else-if="view">{{ warning ? '未记录知悉；此提醒已结束，不能新建知悉记录。' : '未记录知悉；只有持续故障可以新建知悉记录。' }}</p>
  </section>
</template>

<style scoped>
.incident-ack{padding:16px;border:1px solid var(--border);border-radius:12px;display:grid;gap:10px;overflow-wrap:anywhere}.incident-ack header{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.incident-ack h4,.incident-ack h5,.incident-ack p{margin:0;line-height:1.6}.incident-ack form,.ack-recovery,.ack-recovery details{display:grid;gap:10px}.ack-recovery{padding:12px;border:1px solid var(--border);border-radius:10px}.ack-note{white-space:pre-wrap;color:var(--text-primary)!important}.ack-recovery label{display:flex;align-items:start;gap:8px}@media(max-width:560px){.incident-ack{padding:12px}}
</style>
