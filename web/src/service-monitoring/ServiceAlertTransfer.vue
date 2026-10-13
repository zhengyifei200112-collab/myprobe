<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { AdminAPIError, applyServiceAlertImport, exportServiceAlertBundle, loadServiceAlertDestinations, previewServiceAlertImport } from '../admin-api'
import type { ServiceAlertBundle, ServiceAlertDestination, ServiceAlertImportMapping, ServiceAlertImportPreview, ServiceAlertParameters } from '../admin-api'
import ServiceAlertMapping from './ServiceAlertMapping.vue'

const emit = defineEmits<{ close: []; imported: [] }>()
const busy = ref(false), error = ref(''), result = ref(''), attempted = ref(false), invalidRecovery = ref(false)
const bundle = ref<ServiceAlertBundle | null>(null), preview = ref<ServiceAlertImportPreview | null>(null)
const services = ref<ServiceAlertDestination[]>([]), channels = ref<ServiceAlertDestination[]>([])
const mapping = reactive<ServiceAlertImportMapping>({ services: Object.create(null), channels: Object.create(null) })
const requestID = ref(''), execution = ref<boolean | null>(null), heading = ref<HTMLElement | null>(null)
const storageKey = 'myprobe-service-alert-import-pending-v1'
const previewPage = ref(1)
const previewPages = computed(() => Math.max(1, Math.ceil((preview.value?.items.length || 0) / 25)))
const visiblePreview = computed(() => preview.value?.items.slice((previewPage.value - 1) * 25, previewPage.value * 25) || [])
let active = true
onUnmounted(() => { active = false })
const duplicateServices = computed(() => {
  const selected = bundle.value?.alerts.map(v => mapping.services[v.source_id]).filter(Boolean) || []
  return new Set(selected).size !== selected.length
})
const complete = computed(() => !duplicateServices.value && !!bundle.value?.alerts.length && bundle.value.alerts.every(v => !!mapping.services[v.source_id] && !!mapping.channels[v.channel_id]))
function parameters(p: ServiceAlertParameters) {
  return `故障通知${p.enabled ? '开启' : '关闭'}，证书提醒${p.certificate_reminders_enabled ? '开启' : '关闭'}；触发 ${p.duration_seconds} 秒、恢复 ${p.recovery_seconds} 秒、重复 ${p.repeat_seconds === 0 ? '使用冷却间隔' : `${p.repeat_seconds} 秒`}、冷却 ${p.cooldown_seconds} 秒`
}
function validParameters(p: ServiceAlertParameters) {
  return p && typeof p.enabled === 'boolean' && typeof p.certificate_reminders_enabled === 'boolean' && ['duration_seconds', 'recovery_seconds', 'repeat_seconds', 'cooldown_seconds'].every(key => Number.isSafeInteger(p[key as keyof ServiceAlertParameters])) && p.duration_seconds >= 0 && p.duration_seconds <= 2592000 && p.recovery_seconds >= 0 && p.recovery_seconds <= 2592000 && (p.repeat_seconds === 0 || p.repeat_seconds >= 30) && p.repeat_seconds <= 2592000 && p.cooldown_seconds >= 30 && p.cooldown_seconds <= 2592000
}
function validateBundle(value: ServiceAlertBundle) {
  if (value?.format !== 'myprobe-service-alerts' || value.version !== 1 || !Array.isArray(value.alerts) || !value.alerts.length || value.alerts.length > 1000 || !Array.isArray(value.channels) || value.channels.length > 1000) throw new Error('请选择包含 1—1000 个服务的 v1 服务告警导出文件。')
  const ids = new Set<string>(), refs = new Set<string>(), used = new Set<string>()
  const id = (v: unknown) => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v)
  for (const c of value.channels) { if (!c || !id(c.id) || refs.has(c.id) || typeof c.name !== 'string' || !['webhook', 'telegram', 'discord', 'smtp'].includes(c.provider)) throw new Error('通道引用无效。'); refs.add(c.id) }
  for (const v of value.alerts) { if (!v || !id(v.source_id) || ids.has(v.source_id) || typeof v.name !== 'string' || !refs.has(v.channel_id) || !validParameters(v.settings)) throw new Error('服务告警文件结构或参数无效。'); ids.add(v.source_id); used.add(v.channel_id) }
  if (refs.size !== used.size) throw new Error('存在未使用的通道引用。')
}
function clearPreview() { preview.value = null; previewPage.value = 1; requestID.value = ''; result.value = ''; error.value = '' }
function selectTarget(kind: 'services' | 'channels', source: string, destination: string) {
  if (busy.value || attempted.value || invalidRecovery.value) return
  mapping[kind][source] = destination; clearPreview()
}
async function downloadMapping() {
  if (!bundle.value || busy.value) return
  const value = { format: 'myprobe-service-alert-mapping', version: 1, services: Object.fromEntries(bundle.value.alerts.map(v => [v.source_id, mapping.services[v.source_id] || ''])), channels: Object.fromEntries(bundle.value.channels.map(v => [v.id, mapping.channels[v.id] || ''])) }
  const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }))
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'myprobe-service-alert-mapping.json'; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
async function readMapping(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file || !bundle.value || attempted.value || invalidRecovery.value) return
  await run(async () => {
    clearPreview()
    if (file.size > 2 * 1024 * 1024) throw new Error('映射文件不能超过 2 MiB。')
    const value = JSON.parse(await file.text())
    if (value?.format !== 'myprobe-service-alert-mapping' || value.version !== 1 || Object.keys(value).some(key => !['format', 'version', 'services', 'channels'].includes(key))) throw new Error('映射文件格式无效。')
    await loadDestinations()
    if (!active || !bundle.value) return
    const next: ServiceAlertImportMapping = { services: Object.assign(Object.create(null), mapping.services), channels: Object.assign(Object.create(null), mapping.channels) }
    for (const key of ['services', 'channels'] as const) {
      const entries = value[key]
      if (!entries || typeof entries !== 'object' || Array.isArray(entries) || Object.keys(entries).length > 1000) throw new Error('映射文件结构无效。')
      const sources = new Set(key === 'services' ? bundle.value.alerts.map(v => v.source_id) : bundle.value.channels.map(v => v.id))
      const destinations = new Set((key === 'services' ? services.value : channels.value).map(v => v.id))
      for (const [source, destination] of Object.entries(entries)) {
        if (!sources.has(source) || typeof destination !== 'string' || (destination !== '' && !destinations.has(destination))) throw new Error('映射包含未知来源或目标，请核对文件。')
        next[key][source] = destination
      }
    }
    const selected = Object.values(next.services).filter(Boolean)
    if (new Set(selected).size !== selected.length) throw new Error('多个来源选择了同一目标服务，映射未应用。')
    Object.assign(mapping, next)
  }); input.value = ''
}
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await action() } catch (e) { if (active) error.value = e instanceof Error ? e.message : '操作失败，请重试。' }
  finally { if (active) busy.value = false }
}
async function loadDestinations() {
  const response = await loadServiceAlertDestinations()
  if (active) { services.value = response.services; channels.value = response.channels; execution.value = response.execution_enabled }
}
async function readFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file || attempted.value || invalidRecovery.value) return
  await run(async () => {
    clearPreview(); bundle.value = null
    if (file.size > 2 * 1024 * 1024) throw new Error('文件不能超过 2 MiB。')
    const value = JSON.parse(await file.text()); validateBundle(value)
    if (!active) return
    Object.assign(mapping, { services: Object.create(null), channels: Object.create(null) }); bundle.value = value
    await loadDestinations()
  }); input.value = ''
}
async function download() {
  await run(async () => {
    const response = await exportServiceAlertBundle()
    const url = URL.createObjectURL(new Blob([JSON.stringify(response, null, 2)], { type: 'application/json' }))
    const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'myprobe-service-alerts.json'; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  })
}
async function inspect() {
  await run(async () => {
    if (!bundle.value || !complete.value || attempted.value) return
    clearPreview()
    const response = await previewServiceAlertImport(bundle.value, mapping)
    if (!active) return
    requestID.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
    preview.value = response.preview; execution.value = response.execution_enabled
  })
}
async function submit() {
  await run(async () => {
    if (!bundle.value || !preview.value || !requestID.value || invalidRecovery.value) return
    // Persist the original request before sending; failed storage must prevent IO.
    // Cache the approved request and preview identity, not every rendered row.
    // This keeps recovery size proportional to the bounded API request itself.
    sessionStorage.setItem(storageKey, JSON.stringify({ version: 2, bundle: bundle.value, mapping, preview: { created: preview.value.created, updated: preview.value.updated, preview_digest: preview.value.preview_digest }, requestID: requestID.value }))
    attempted.value = true
    let response
    try { response = await applyServiceAlertImport(bundle.value, mapping, requestID.value, preview.value.preview_digest) }
    catch (e) {
      if (e instanceof AdminAPIError && (e.status === 400 || (e.status === 409 && e.code !== 'service_alert_import_request_conflict'))) {
        sessionStorage.removeItem(storageKey); attempted.value = false; clearPreview(); throw new Error(`${e.message}。本次请求未更新配置，请重新预览。`)
      }
      throw e
    }
    sessionStorage.removeItem(storageKey)
    if (!active) return
    result.value = `${response.replayed ? '已确认此前导入结果' : '服务告警导入成功'}：新建 ${response.created} 个，更新 ${response.updated} 个。下一轮评估生效，HTTP 执行开关不变。`
    attempted.value = false; bundle.value = null; preview.value = null; requestID.value = ''; emit('imported')
  })
}
function discard() {
  try { sessionStorage.removeItem(storageKey); invalidRecovery.value = false; attempted.value = false; bundle.value = null; clearPreview() }
  catch { error.value = '无法清除浏览器记录，请检查存储权限。' }
}
onMounted(async () => {
  await nextTick(); if (active) heading.value?.focus()
  try {
    const raw = sessionStorage.getItem(storageKey)
    if (raw) {
      if (raw.length > 2 * 1024 * 1024 + 16384) throw new Error('invalid recovery')
      const value = JSON.parse(raw); validateBundle(value.bundle)
      const compact = value.version === 2
      if ((value.version !== undefined && value.version !== 2) || !/^[a-f0-9]{32}$/.test(value.requestID || '') || !/^[a-f0-9]{64}$/.test(value.preview?.preview_digest || '') || (!compact && (!Array.isArray(value.preview.items) || value.preview.items.length !== value.bundle.alerts.length)) || (compact && value.preview.items !== undefined) || !Number.isSafeInteger(value.preview.created) || !Number.isSafeInteger(value.preview.updated) || value.preview.created < 0 || value.preview.updated < 0 || value.preview.created + value.preview.updated !== value.bundle.alerts.length) throw new Error('invalid recovery')
      for (const key of ['services', 'channels'] as const) {
        const m = value.mapping?.[key]
        const expected = key === 'services' ? value.bundle.alerts.map((v: ServiceAlertBundle['alerts'][number]) => v.source_id) : value.bundle.channels.map((v: ServiceAlertBundle['channels'][number]) => v.id)
        if (!m || typeof m !== 'object' || Array.isArray(m) || Object.keys(m).length !== expected.length || !expected.every((id: string) => Object.hasOwn(m, id)) || !Object.entries(m).every(([k, v]) => /^[A-Za-z0-9_-]{1,128}$/.test(k) && typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v))) throw new Error('invalid recovery')
      }
      const selected = new Set(Object.values(value.mapping.services)), sources = new Set<string>()
      if (selected.size !== value.bundle.alerts.length) throw new Error('invalid recovery')
      let created = 0
      for (const item of compact ? [] : value.preview.items) {
        const source = value.bundle.alerts.find((v: ServiceAlertBundle['alerts'][number]) => v.source_id === item?.source_id)
        if (!source || sources.has(item.source_id) || item.service_id !== value.mapping.services[source.source_id] || item.channel_id !== value.mapping.channels[source.channel_id] || typeof item.service_name !== 'string' || typeof item.channel_name !== 'string' || typeof item.service_enabled !== 'boolean' || typeof item.channel_enabled !== 'boolean' || !Number.isSafeInteger(item.service_revision) || item.service_revision < 1 || !Number.isSafeInteger(item.alert_revision) || item.alert_revision < 0 || !Number.isSafeInteger(item.channel_delivery_revision) || item.channel_delivery_revision < 1 || !validParameters(item.after) || !Object.keys(source.settings).every(key => item.after[key] === source.settings[key]) || (item.before !== null && !validParameters(item.before))) throw new Error('invalid recovery')
        sources.add(item.source_id); if (item.before === null) created++
      }
      if (!compact && created !== value.preview.created) throw new Error('invalid recovery')
      bundle.value = value.bundle; Object.assign(mapping, value.mapping); preview.value = { ...value.preview, items: compact ? [] : value.preview.items }; requestID.value = value.requestID; attempted.value = true
    }
  } catch { invalidRecovery.value = true; error.value = '无法读取待确认导入记录。请先核对服务告警与审计，再清除本地记录；清除不会撤销已提交配置。' }
  try { await loadDestinations() } catch (e) { if (active && !invalidRecovery.value) error.value = e instanceof Error ? e.message : '无法读取目标服务与通道。' }
})
</script>

<template>
  <section class="admin-panel service-alert-transfer" aria-label="服务告警迁移">
    <h2 ref="heading" tabindex="-1">服务告警配置迁移</h2>
    <p>先导入或创建服务定义和通知通道，再明确匹配此处的服务告警。导出只含别名、引用及提醒参数，不包含目标、凭据或运行历史。</p>
    <div class="transfer-actions"><button :disabled="busy" class="soft-button" @click="download">导出服务告警</button><label>服务告警文件<input type="file" accept=".json,application/json" :disabled="busy || attempted || invalidRecovery" @change="readFile"></label></div>
    <p v-if="error" role="alert" class="form-message error">{{ error }}</p><p v-if="result" role="status" class="form-message success">{{ result }}</p>
    <p v-if="attempted" class="form-message">上次请求的结果尚待确认。文件与映射已冻结；请确认原请求，避免重复覆盖。关闭页面不会撤销已提交配置。</p>
    <p v-if="invalidRecovery" class="form-message">待确认记录损坏，提交已暂停。</p><button v-if="invalidRecovery" :disabled="busy" class="soft-button" @click="discard">清除损坏的导入记录</button>
    <template v-if="bundle">
      <div class="transfer-actions"><button type="button" class="soft-button" :disabled="busy || attempted || invalidRecovery" @click="downloadMapping">下载映射模板</button><label>服务告警映射文件<input type="file" accept=".json,application/json" :disabled="busy || attempted || invalidRecovery" @change="readMapping"></label></div>
      <p>映射文件由你明确填写目标 ID；空值清除对应选择，未列出的来源保留现有选择。上传后仍须核对完整预览，系统不会按名称或相同 ID 自动匹配。</p>
      <ServiceAlertMapping :bundle="bundle" :services="services" :channels="channels" :mapping="mapping" :disabled="busy || attempted || invalidRecovery" @select="selectTarget" />
      <p v-if="duplicateServices" role="alert">多个来源选择了同一目标服务，请为每个来源选择不同的服务。</p>
      <button type="button" class="primary-button" :disabled="busy || attempted || invalidRecovery || !complete" @click="inspect">预览服务告警导入</button>
    </template>
    <section v-if="preview" class="transfer-preview" aria-label="服务告警导入预览">
      <h3>新建 {{ preview.created }} 个 · 覆盖 {{ preview.updated }} 个</h3>
      <p v-if="execution === false">服务端 HTTP 执行未启用；导入不会开启自动检查。</p>
      <p v-if="attempted && preview.items.length">下方保留原预览，服务与通道状态可能已经变化；确认原请求会返回此前结果，不重新覆盖。</p>
      <article v-for="item in visiblePreview" :key="item.source_id"><h4>{{ item.service_name }} → {{ item.channel_name }}（{{ item.channel_provider }}）</h4><p v-if="item.before">原配置：{{ parameters(item.before) }}；通道 {{ item.before_channel_name }}（{{ item.before_channel_provider }}）</p><p v-else>原配置：尚未设置服务告警</p><p>导入后：{{ parameters(item.after) }}</p><p v-if="!item.service_enabled">此服务当前停用，提醒不改变服务启用状态。</p><p v-if="!item.channel_enabled">目标通道停用，技术事件仍可记录，当前不会发送通知。</p><p v-if="bundle?.channels.find(c => c.id === bundle?.alerts.find(a => a.source_id === item.source_id)?.channel_id)?.provider !== item.channel_provider">来源与目标 Provider 类型不同，请核对选择。</p></article>
      <p v-if="!preview.items.length && attempted">原请求及预览版本已固定。恢复页面不重建旧预览，确认将返回原结果；尚未提交时服务端仍会校验原预览版本。</p>
      <div v-if="previewPages > 1" class="transfer-pagination"><button type="button" :disabled="previewPage === 1" @click="previewPage--">预览上一页</button><span>预览第 {{ previewPage }} / {{ previewPages }} 页 · {{ preview.items.length }} 项</span><button type="button" :disabled="previewPage === previewPages" @click="previewPage++">预览下一页</button></div>
      <p class="form-message">确认会完整覆盖所选服务的提醒参数。启用故障或证书提醒后，下一轮评估可能依据已有新鲜证据发出通知；其他服务和 HTTP 执行开关不变。</p>
      <button class="primary-button" :disabled="busy || invalidRecovery" @click="submit">{{ attempted ? '确认上次服务告警导入结果' : '确认覆盖服务告警' }}</button>
    </section>
    <button class="soft-button" :disabled="busy" @click="emit('close')">关闭迁移</button>
  </section>
</template>

<style scoped>
.service-alert-transfer { padding: 24px; display: grid; grid-template-columns: minmax(0,1fr); gap: 18px; min-width: 0; overflow-wrap: anywhere; }
.service-alert-transfer :is(select,button,input,fieldset,article) { box-sizing: border-box; }
.service-alert-transfer h2,.service-alert-transfer h3,.service-alert-transfer h4,.service-alert-transfer p { margin: 0; }
.service-alert-transfer p { line-height: 1.6; }
.transfer-actions { display: flex; gap: 14px; flex-wrap: wrap; align-items: center; }
.transfer-actions label { display: grid; gap: 8px; min-width: 0; }
.transfer-actions input { max-width: 100%; }
.transfer-mapping { padding: 0; margin: 0; border: 0; display: grid; grid-template-columns: minmax(0,1fr); gap: 14px; min-width: 0; }
.transfer-mapping legend { margin-bottom: 14px; font-weight: 600; }
.transfer-mapping label { display: grid; gap: 8px; min-width: 0; }
.transfer-mapping select { box-sizing: border-box; min-width: 0; width: 100%; min-height: 40px; padding: 8px 10px; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
.transfer-preview { display: grid; grid-template-columns: minmax(0,1fr); min-width: 0; gap: 14px; }
.transfer-preview article { display: grid; gap: 10px; padding: 16px; border: 1px solid var(--border); border-radius: 12px; min-width: 0; }
.transfer-mapping button,.service-alert-transfer > button { justify-self: start; }
@media(max-width:600px) { .service-alert-transfer { padding: 16px; } .service-alert-transfer h2 { font-size: 20px; } }
</style>
