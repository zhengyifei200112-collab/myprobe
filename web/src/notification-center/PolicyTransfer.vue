<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import type { NodeMetadata } from '../types'
import type { NotificationChannel, NotificationTemplate, PolicyBundle, PolicyImportMapping, PolicyImportPreview, PortablePolicy } from '../admin-api'
import { AdminAPIError, applyPolicyImport, exportPolicyBundle, loadNotificationTemplates, previewPolicyImport } from '../admin-api'
const props = defineProps<{ nodes: NodeMetadata[]; channels: NotificationChannel[] }>()
const emit = defineEmits<{ imported: [] }>()
const bundle = ref<PolicyBundle | null>(null), preview = ref<PolicyImportPreview | null>(null)
const templates = ref<NotificationTemplate[]>([]), busy = ref(false), error = ref(''), result = ref('')
const mapping = reactive<PolicyImportMapping>({ channels: Object.create(null), nodes: Object.create(null), templates: Object.create(null) })
const requestID = ref(''), attempted = ref(false)
const recoveryInvalid = ref(false)
const kinds = { cpu: 'CPU', memory: '内存', disk: '磁盘', offline: '离线', latency: '延迟', bandwidth: '带宽', cycle_traffic: '周期流量', expiry: '到期' }
function scopeText(p: PortablePolicy) {
  if (p.scope.kind === 'all') return '全部节点'
  if (p.scope.kind === 'nodes') return p.scope.node_ids.map(id => props.nodes.find(n => n.id === id)?.name ?? id).join('、')
  return `${p.scope.tag_mode === 'all' ? '包含全部标签' : '包含任意标签'}：${p.scope.tags.join('、')}`
}
function thresholdText(p: PortablePolicy) {
  const c = p.config
  switch (p.kind) {
    case 'cpu': case 'memory': case 'disk': return `${c.threshold_percent} %`
    case 'offline': return `${c.offline_seconds} 秒未上报`
    case 'latency': return `${c.threshold_milliseconds} 毫秒`
    case 'bandwidth': return `${c.threshold_bytes_per_second} 字节/秒`
    case 'cycle_traffic': return `${c.threshold_bytes} 字节`
    case 'expiry': return `提前 ${c.days_before} 天`
  }
}
function validateBundle(value: PolicyBundle) {
  if (value?.format !== 'myprobe-alert-policies' || value.version !== 1 || !Array.isArray(value.policies) || !value.policies.length || value.policies.length > 1000) throw new Error('请选择包含 1—1000 条策略的 v1 策略导出文件。')
  for (const p of value.policies) {
    if (p?.scope?.kind === 'tags' && (!Array.isArray(p.scope.tags) || !p.scope.tags.every((tag: unknown) => typeof tag === 'string') || !['all', 'any'].includes(p.scope.tag_mode))) throw new Error('标签范围无效。')
    if (!p || typeof p.source_id !== 'string' || typeof p.name !== 'string' || typeof p.channel_id !== 'string' || !p.scope || !['all', 'nodes', 'tags'].includes(p.scope.kind) || !p.config || typeof p.config !== 'object' || Array.isArray(p.config) || (p.scope.kind === 'nodes' && (!Array.isArray(p.scope.node_ids) || !p.scope.node_ids.every((id: unknown) => typeof id === 'string'))) || (p.config.template_id != null && typeof p.config.template_id !== 'string')) throw new Error('策略文件结构无效。')
  }
}
const references = computed(() => {
  const channels = new Set<string>(), nodes = new Set<string>(), templates = new Set<string>()
  for (const p of bundle.value?.policies ?? []) {
    channels.add(p.channel_id)
    if (p.scope.kind === 'nodes') p.scope.node_ids.forEach(id => nodes.add(id))
    if (p.config.template_id) templates.add(p.config.template_id)
  }
  return { channels: [...channels], nodes: [...nodes], templates: [...templates] }
})
const complete = computed(() => Object.entries(references.value).every(([kind, ids]) => ids.every(id => !!mapping[kind as keyof PolicyImportMapping][id])))
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await action() } catch (e) { error.value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = false }
}
function invalidate() { preview.value = null; requestID.value = ''; result.value = '' }
async function readFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file) return
  await run(async () => {
    invalidate(); bundle.value = null
    if (file.size > 16 * 1024 * 1024) throw new Error('文件不能超过 16 MiB。')
    const value = JSON.parse(await file.text())
    validateBundle(value)
    Object.assign(mapping, { channels: Object.create(null), nodes: Object.create(null), templates: Object.create(null) })
    bundle.value = value
    templates.value = (await loadNotificationTemplates()).templates
  })
  input.value = ''
}
async function download() {
  await run(async () => {
    const data = await exportPolicyBundle()
    const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
    const a = document.createElement('a'); a.href = url; a.download = 'myprobe-alert-policies.json'; a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  })
}
async function inspect() {
  await run(async () => {
    if (!bundle.value || !complete.value) return
    preview.value = null
    const response = await previewPolicyImport(bundle.value, mapping)
    // getRandomValues is available on direct HTTP deployments too; randomUUID
    // requires a secure context. Keep 128 bits of randomness without weak fallback.
    requestID.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
    preview.value = response
  })
}
const storageKey = 'myprobe-policy-import-pending-v1'
async function submit() {
  await run(async () => {
    if (!bundle.value || !preview.value || !requestID.value) return
    // Persist before sending: refresh or a lost response must retain the same key.
    sessionStorage.setItem(storageKey, JSON.stringify({ bundle: bundle.value, mapping, preview: preview.value, requestID: requestID.value }))
    attempted.value = true
    let response
    try {
      response = await applyPolicyImport(bundle.value, mapping, requestID.value, preview.value.preview_digest)
    } catch (e) {
      // A transport/server error may follow a committed transaction. Keep the
      // original request until replay resolves that uncertainty.
      if (e instanceof AdminAPIError && (e.status === 400 || (e.status === 409 && e.code !== 'policy_import_request_conflict'))) {
        sessionStorage.removeItem(storageKey)
        attempted.value = false; invalidate()
        throw new Error(`${e.message}。本次请求未创建策略，请调整配置后重新预览。`)
      }
      throw e
    }
    sessionStorage.removeItem(storageKey)
    result.value = `${response.replayed ? '已确认此前导入结果' : '导入成功'}：${response.policy_ids.length} 条策略。配置将在下一轮评估时应用，通常间隔 15 秒。`
    bundle.value = null; preview.value = null; attempted.value = false; requestID.value = ''
    emit('imported')
  })
}
function discardRecovery() {
  try {
    sessionStorage.removeItem(storageKey); recoveryInvalid.value = false; error.value = ''
  } catch { error.value = '无法清除浏览器记录，请检查存储权限。' }
}
onMounted(() => {
  try {
    const raw = sessionStorage.getItem(storageKey)
    if (!raw) return
    const pending = JSON.parse(raw)
    validateBundle(pending?.bundle)
    validateBundle(pending?.preview?.bundle)
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(pending.requestID ?? '') || !/^[a-f0-9]{64}$/.test(pending.preview.preview_digest ?? '') || pending.preview.create_count !== pending.bundle.policies.length) throw new Error('invalid recovery identity')
    for (const key of ['channels', 'nodes', 'templates']) {
      const values = pending.mapping?.[key]
      if (!values || typeof values !== 'object' || Array.isArray(values) || !Object.values(values).every(v => typeof v === 'string')) throw new Error('invalid recovery mapping')
    }
    bundle.value = pending.bundle; Object.assign(mapping, pending.mapping); preview.value = pending.preview; requestID.value = pending.requestID; attempted.value = true
    void loadNotificationTemplates().then(response => { templates.value = response.templates }).catch(() => { /* Existing IDs still suffice for an identical retry. */ })
  } catch { recoveryInvalid.value = true; error.value = '无法读取待确认导入记录。请先核对策略列表和审计记录，确认上次导入结果；清除本地记录不会撤销已提交的策略。' }
})
</script>

<template>
  <details class="policy-transfer">
    <summary>策略导入与导出</summary>
    <p>只新增策略，不覆盖现有策略。文件不含通知凭据和故障历史；完整迁移请使用数据库备份。</p>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="result" role="status">{{ result }}</p>
    <button v-if="recoveryInvalid" @click="discardRecovery">已核对导入结果，清除损坏的本地记录</button>
    <button :disabled="busy" @click="download">导出策略文件</button>
    <label>导入策略文件<input type="file" accept="application/json,.json" :disabled="busy || attempted || recoveryInvalid" @change="readFile"></label>
    <template v-if="bundle">
      <p>文件包含 {{ bundle.policies.length }} 条策略。请选择目的地引用；不会按名称自动匹配。</p>
      <fieldset :disabled="busy || attempted" @change="invalidate">
        <legend>引用映射</legend>
        <label v-for="id in references.channels" :key="'channel'+id">渠道：{{ id }}<select v-model="mapping.channels[id]"><option value="">选择目的地渠道</option><option v-for="c in channels" :key="c.id" :value="c.id">{{ c.name }}</option></select></label>
        <label v-for="id in references.nodes" :key="'node'+id">节点：{{ id }}<select v-model="mapping.nodes[id]"><option value="">选择目的地节点</option><option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}</option></select></label>
        <label v-for="id in references.templates" :key="'template'+id">模板：{{ id }}<select v-model="mapping.templates[id]"><option value="">选择目的地模板</option><option v-for="t in templates" :key="t.id" :value="t.id">{{ t.name }}</option></select></label>
      </fieldset>
      <button v-if="!attempted" :disabled="busy || !complete" @click="inspect">预览导入</button>
      <section v-if="preview" aria-label="导入预览">
        <h3>将新增 {{ preview.create_count }} 条策略</h3>
        <p>标签范围会随节点标签动态匹配。预览不锁定渠道、模板或节点；提交时再次校验。</p>
        <details v-for="p in preview.bundle.policies" :key="p.source_id">
          <summary>{{ p.name }} · {{ p.enabled ? '启用' : '停用' }}</summary>
          <dl class="policy-preview">
            <div><dt>策略键 / 优先级</dt><dd>{{ p.policy_key }} / {{ p.priority }}</dd></div>
            <div><dt>匹配范围</dt><dd>{{ scopeText(p) }}</dd></div>
            <div><dt>通知渠道</dt><dd>{{ channels.find(c => c.id === p.channel_id)?.name ?? p.channel_id }}</dd></div>
            <div><dt>消息模板</dt><dd>{{ p.config.template_id ? (templates.find(t => t.id === p.config.template_id)?.name ?? p.config.template_id) : '默认模板' }}</dd></div>
            <div><dt>{{ kinds[p.kind] }}阈值</dt><dd>{{ thresholdText(p) }}</dd></div>
            <div><dt>触发持续时间</dt><dd>{{ p.config.duration_seconds ?? 0 }} 秒</dd></div>
            <div><dt>恢复持续时间</dt><dd>{{ p.config.recovery_seconds ?? p.config.duration_seconds ?? 0 }} 秒</dd></div>
            <div><dt>重复提醒间隔</dt><dd>{{ p.config.repeat_seconds || p.cooldown_seconds }} 秒</dd></div>
            <div><dt>冷却时间</dt><dd>{{ p.cooldown_seconds }} 秒</dd></div>
          </dl>
        </details>
        <p v-if="attempted">上次提交结果尚待确认。请使用原请求重试，避免重复创建；刷新后可继续确认。</p>
        <button :disabled="busy" @click="submit">{{ attempted ? '重试确认导入结果' : '确认新增策略' }}</button>
      </section>
    </template>
  </details>
</template>

<style scoped>
.policy-transfer { margin-block: 1rem; padding: 1rem; border: 1px solid var(--border); background: var(--surface-subtle); border-radius: 12px; overflow-wrap: anywhere; }
label { display: grid; gap: .4rem; margin-block: .8rem; }
fieldset { min-width: 0; margin-block: 1rem; }
select, input { width: 100%; max-width: 100%; min-width: 0; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-subtle); color: inherit; font: inherit; }
.policy-preview { display: grid; gap: .7rem; padding: .8rem; border: 1px solid var(--border); border-radius: 10px; }
.policy-preview dt { font-size: .85rem; opacity: .75; }
.policy-preview dd { margin: .2rem 0 0; }
button { margin: .4rem .5rem .4rem 0; padding: 10px 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-subtle); color: inherit; font: inherit; cursor: pointer; }
button:disabled { opacity: .5; cursor: default; }
button:focus-visible, select:focus-visible, input:focus-visible, summary:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
summary { cursor: pointer; }
</style>
