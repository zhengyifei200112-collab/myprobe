<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import type { NodeMetadata } from '../types'
import type { NotificationChannel, NotificationTemplate, PolicyBundle, PolicyImportMapping, PolicyImportPreview } from '../admin-api'
import { AdminAPIError, applyPolicyImport, exportPolicyBundle, loadNotificationTemplates, previewPolicyImport } from '../admin-api'
defineProps<{ nodes: NodeMetadata[]; channels: NotificationChannel[] }>()
const emit = defineEmits<{ imported: [] }>()
const bundle = ref<PolicyBundle | null>(null), preview = ref<PolicyImportPreview | null>(null)
const templates = ref<NotificationTemplate[]>([]), busy = ref(false), error = ref(''), result = ref('')
const mapping = reactive<PolicyImportMapping>({ channels: Object.create(null), nodes: Object.create(null), templates: Object.create(null) })
const requestID = ref(''), attempted = ref(false)
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
    if (value?.format !== 'myprobe-alert-policies' || value.version !== 1 || !Array.isArray(value.policies) || !value.policies.length || value.policies.length > 1000) throw new Error('请选择包含 1—1000 条策略的 v1 策略导出文件。')
    for (const p of value.policies) {
      if (!p || typeof p.channel_id !== 'string' || !p.scope || !['all', 'nodes', 'tags'].includes(p.scope.kind) || !p.config || (p.scope.kind === 'nodes' && (!Array.isArray(p.scope.node_ids) || !p.scope.node_ids.every((id: unknown) => typeof id === 'string'))) || (p.config.template_id != null && typeof p.config.template_id !== 'string')) throw new Error('策略文件结构无效。')
    }
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
    preview.value = await previewPolicyImport(bundle.value, mapping)
    requestID.value = crypto.randomUUID()
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
onMounted(() => {
  try {
    const raw = sessionStorage.getItem(storageKey)
    if (!raw) return
    const pending = JSON.parse(raw)
    bundle.value = pending.bundle; Object.assign(mapping, pending.mapping); preview.value = pending.preview; requestID.value = pending.requestID; attempted.value = true
  } catch { error.value = '无法读取待确认导入记录。请检查浏览器存储。' }
})
</script>

<template>
  <details class="policy-transfer">
    <summary>策略导入与导出</summary>
    <p>只新增策略，不覆盖现有策略。文件不含通知凭据和故障历史；完整迁移请使用数据库备份。</p>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="result" role="status">{{ result }}</p>
    <button :disabled="busy" @click="download">导出策略文件</button>
    <label>导入策略文件<input type="file" accept="application/json,.json" :disabled="busy || attempted" @change="readFile"></label>
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
        <details v-for="p in preview.bundle.policies" :key="p.source_id"><summary>{{ p.name }} · {{ p.enabled ? '启用' : '停用' }}</summary><pre>{{ JSON.stringify(p, null, 2) }}</pre></details>
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
pre { white-space: pre-wrap; overflow-wrap: anywhere; font-size: .8rem; }
button { margin: .4rem .5rem .4rem 0; padding: 10px 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-subtle); color: inherit; font: inherit; cursor: pointer; }
button:disabled { opacity: .5; cursor: default; }
button:focus-visible, select:focus-visible, input:focus-visible, summary:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
summary { cursor: pointer; }
</style>
