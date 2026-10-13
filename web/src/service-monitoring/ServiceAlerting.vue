<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { loadChannels, loadServiceAlerting, loadServiceIncidents, saveServiceAlerting } from '../admin-api'
import type { HTTPServiceSummary, Incident, NotificationChannel, ServiceAlertSettings } from '../admin-api'
const props = defineProps<{ service: HTTPServiceSummary }>()
const emit = defineEmits<{ close: [] }>()
const settings = ref<ServiceAlertSettings | null>(null), channels = ref<NotificationChannel[]>([]), events = ref<Incident[]>([])
const busy = ref(false), error = ref(''), notice = ref(''), execution = ref(false), next = ref(0)
let active = true
onUnmounted(() => { active = false })
async function refresh() {
  const [configuration, channelData, eventData] = await Promise.all([loadServiceAlerting(props.service.id), loadChannels(), loadServiceIncidents(props.service.id)])
  if (!active) return
  if (!Number.isSafeInteger(configuration.alerting.revision) || !Number.isSafeInteger(configuration.alerting.service_revision)) throw new Error('配置版本超出浏览器可安全处理的范围，不能在此保存。')
  settings.value = configuration.alerting; execution.value = configuration.execution_enabled
  channels.value = channelData.channels; events.value = eventData.incidents; next.value = eventData.next_before
}
async function run(action: () => Promise<void>) {
  busy.value = true; error.value = ''; notice.value = ''
  try { await action() } catch (e) { if (active) error.value = e instanceof Error ? e.message : '服务告警暂时无法处理，请重试。' }
  finally { if (active) busy.value = false }
}
async function save() {
  await run(async () => {
    if (!settings.value) return
    const response = await saveServiceAlerting(props.service.id, settings.value)
    if (!active) return
    settings.value = response.alerting; execution.value = response.execution_enabled
    notice.value = '服务告警配置已保存，下一轮评估后生效。'
  })
}
async function older() {
  await run(async () => {
    const response = await loadServiceIncidents(props.service.id, next.value)
    if (!active) return
    events.value.push(...response.incidents); next.value = response.next_before
  })
}
function status(event: Incident) {
  if (event.state === 'resolved') return '事件已结束'
  if (event.observation_stale) return '等待新鲜观测，当前故障状态未确认恢复'
  return event.state === 'firing' ? '故障持续' : '等待触发'
}
onMounted(() => run(refresh))
</script>

<template>
  <section class="admin-panel service-alerting" aria-label="服务告警与事件">
    <h2>{{ service.name }} · 服务告警与事件</h2>
    <p>每个观测点独立记录故障与恢复。缺测、离线、不支持或未执行保持未知；没有故障事件不代表检查成功，请结合最近结果与覆盖率判断。</p>
    <div class="form-actions"><button :disabled="busy" @click="run(refresh)">刷新服务告警</button><button @click="emit('close')">关闭服务告警</button></div>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p><p v-if="busy" role="status">正在处理服务告警…</p>
    <form v-if="settings" @submit.prevent="save">
      <fieldset :disabled="busy">
        <legend>通知配置</legend>
        <p v-if="!execution">服务端 HTTP 执行尚未启用，保存告警配置不会启动检查。请启用执行并确认 Agent 支持。</p>
        <label class="switch-row"><input v-model="settings.enabled" type="checkbox">启用服务故障通知</label>
        <div class="form-grid two">
          <label>通知通道<select v-model="settings.channel_id" required><option value="" disabled>选择通知通道</option><option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channel.name }}{{ channel.enabled ? '' : '（已停用）' }}</option></select></label>
          <label>触发持续时间（秒）<input v-model.number="settings.duration_seconds" type="number" min="0" max="2592000" step="1" required></label>
          <label>恢复持续时间（秒）<input v-model.number="settings.recovery_seconds" type="number" min="0" max="2592000" step="1" required></label>
          <label>重复提醒（秒，0 使用冷却间隔）<input v-model.number="settings.repeat_seconds" type="number" min="0" max="2592000" step="1" required></label>
          <label>冷却间隔（秒）<input v-model.number="settings.cooldown_seconds" type="number" min="30" max="2592000" step="1" required></label>
        </div>
        <p>触发与恢复的 0 表示立即确认；重复提醒须为 0 或至少 30 秒。通知使用现有通道，遵守事件静默与观测节点维护。</p>
        <button class="primary-button" :disabled="!settings.channel_id">保存服务告警</button>
      </fieldset>
    </form>
    <h3>服务故障事件</h3>
    <p v-if="settings && !events.length && !busy">暂无服务故障事件。</p>
    <article v-for="event in events" :key="event.id">
      <a :href="`/admin?incident=${encodeURIComponent(event.id)}`">{{ event.node_name }} · 查看事件</a>
      <p>{{ status(event) }}</p><p>{{ event.message }}</p>
    </article>
    <button v-if="next" :disabled="busy" @click="older">加载较早服务事件</button>
  </section>
</template>

<style scoped>
.service-alerting{display:grid;gap:20px;padding:24px;overflow-wrap:anywhere}.service-alerting h2,.service-alerting h3,.service-alerting p{margin:0}.service-alerting h2{font-size:22px}.service-alerting h3{font-size:17px}.service-alerting form,.service-alerting fieldset{display:grid;gap:16px;min-width:0}.service-alerting fieldset{margin:0;padding:16px;border:1px solid var(--border);border-radius:12px}.service-alerting legend{padding:0 6px;font-size:14px;font-weight:700}.service-alerting article{display:grid;gap:12px;padding:16px;border:1px solid var(--border);border-radius:12px}.service-alerting p{line-height:1.6}.service-alerting a{color:var(--blue)}.service-alerting input,.service-alerting select{max-width:100%;min-width:0}.service-alerting .switch-row{margin:0;font-size:13px;gap:8px}.service-alerting .form-actions{flex-wrap:wrap}@media(max-width:600px){.service-alerting{padding:16px}.service-alerting h2{font-size:20px}}
</style>
