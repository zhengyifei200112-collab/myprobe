<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import type { NodeMetadata } from '../types'
import { deleteHTTPService, listHTTPServices, loadHTTPService, saveHTTPService } from '../admin-api'
import type { HTTPServiceConfig, HTTPServiceSummary } from '../admin-api'
import { DsConfirmDialog } from '../design-system'

defineProps<{ nodes: NodeMetadata[] }>()
const items = ref<HTTPServiceSummary[]>([])
const cursor = ref('')
const busy = ref(false)
const loaded = ref(false)
const execution = ref(false)
const error = ref('')
const notice = ref('')
const editing = ref(false)
const deleting = ref<HTTPServiceSummary | null>(null)
let active = true
onUnmounted(() => { active = false })
const defaults = () => ({ id: '', revision: 0, name: '', enabled: true, interval: 60, url: '', method: 'GET' as 'GET' | 'HEAD', statuses: '200', timeout: 5, redirects: 3, bodyKiB: 1024, nodeIDs: [] as string[], assertion: 'none', text: '', jsonPath: '["status"]', expected: '"ok"' })
const form = reactive(defaults())
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await action() } catch (e) { if (active) error.value = e instanceof Error ? e.message : '操作失败，请重试。' }
  finally { if (active) busy.value = false }
}
async function refresh(more = false) {
  const result = await listHTTPServices(more ? cursor.value : '')
  if (!active) return
  items.value = more ? [...items.value, ...result.services.filter(item => !items.value.some(old => old.id === item.id))] : result.services
  cursor.value = result.next_cursor; execution.value = result.execution_enabled; loaded.value = true
}
function create() { Object.assign(form, defaults()); editing.value = true; error.value = ''; notice.value = '' }
async function edit(id: string) {
  await run(async () => {
    const { service, execution_enabled } = await loadHTTPService(id)
    if (!active) return
    Object.assign(form, defaults(), { id, revision: service.revision, name: service.name, enabled: service.enabled, interval: service.interval_seconds, url: service.spec.url, method: service.spec.method, statuses: service.spec.status_codes.join(', '), timeout: service.spec.timeout_ms / 1000, redirects: service.spec.max_redirects, bodyKiB: service.spec.max_body_bytes / 1024, nodeIDs: [...service.node_ids], assertion: service.spec.assertion?.kind || 'none', text: service.spec.assertion?.text || '', jsonPath: JSON.stringify(service.spec.assertion?.path || ['status']), expected: service.spec.assertion?.kind === 'json_equals' ? JSON.stringify(service.spec.assertion.expected) : '"ok"' })
    execution.value = execution_enabled; editing.value = true
    if (service.expected_json !== undefined) form.expected = service.expected_json
  })
}
async function save() {
  await run(async () => {
    const codes = form.statuses.split(',').map(value => value.trim())
    if (codes.some(value => !/^\d{3}$/.test(value))) throw new Error('状态码请使用逗号分隔的三位整数。')
    if (form.timeout >= form.interval) throw new Error('超时必须小于检查间隔。')
    const value: HTTPServiceConfig = { id: form.id || undefined, revision: form.revision, name: form.name, enabled: form.enabled, interval_seconds: form.interval, node_ids: [...form.nodeIDs], spec: { url: form.url, method: form.method, status_codes: codes.map(Number), timeout_ms: Math.round(form.timeout * 1000), max_redirects: form.redirects, max_body_bytes: Math.round(form.bodyKiB * 1024) } }
    if (form.method === 'GET' && form.assertion === 'text_contains') value.spec.assertion = { kind: 'text_contains', text: form.text }
    if (form.method === 'GET' && form.assertion === 'json_equals') {
      let path: unknown, expected: unknown
      try { path = JSON.parse(form.jsonPath); expected = JSON.parse(form.expected) } catch { throw new Error('JSON 字段路径和预期值必须是有效 JSON。') }
      if (!Array.isArray(path) || path.some(key => typeof key !== 'string')) throw new Error('字段路径必须是由对象键组成的 JSON 字符串数组。')
      if (expected !== null && typeof expected === 'object') throw new Error('预期值只能是字符串、数字、布尔值或 null。')
      value.spec.assertion = { kind: 'json_equals', path, expected }
      value.expected_json = form.expected.trim()
    }
    const result = await saveHTTPService(value)
    if (!active) return
    execution.value = result.execution_enabled; editing.value = false
    notice.value = '服务配置已保存。实际检查取决于服务端调度和 Agent 能力。'
    await refresh()
  })
}
async function remove() {
  const item = deleting.value
  if (!item) return
  await run(async () => {
    await deleteHTTPService(item.id, item.revision)
    if (!active) return
    deleting.value = null; notice.value = '服务及其历史数据已删除。'; await refresh()
  })
}
onMounted(() => run(() => refresh()))
</script>

<template>
  <section class="service-monitoring">
    <header class="admin-heading"><div><span class="eyebrow">SERVICE MONITORING</span><h1>HTTP 服务</h1><p>从选定节点检查网站响应、内容与 HTTPS 证书。</p></div><button class="primary-button" :disabled="busy" @click="create">新建服务</button></header>
    <p v-if="loaded && !execution" class="form-message" role="status">服务端 HTTP 探测尚未启用。可以保存配置，当前不会自动执行检查。</p>
    <p v-if="error" class="form-message error" role="alert">{{ error }}</p>
    <p v-if="notice" class="form-message success" role="status">{{ notice }}</p>
    <form v-if="editing" class="admin-panel compact-form" @submit.prevent="save">
      <h2>{{ form.id ? '编辑服务' : '新建 HTTP 服务' }}</h2>
      <fieldset :disabled="busy" class="service-fields">
        <div class="form-grid two">
          <label>服务名称<input v-model="form.name" required maxlength="128"></label>
          <label>目标 URL<input v-model="form.url" type="url" required maxlength="2048" placeholder="https://example.com/health"></label>
          <label>请求方式<select v-model="form.method"><option>GET</option><option>HEAD</option></select></label>
          <label>允许状态码<input v-model="form.statuses" required placeholder="200, 204"></label>
          <label>检查间隔（秒）<input v-model.number="form.interval" type="number" min="30" max="86400" step="1" required></label>
          <label>总超时（秒）<input v-model.number="form.timeout" type="number" min="0.1" max="60" step="0.001" required></label>
          <label>最多重定向次数<input v-model.number="form.redirects" type="number" min="0" max="3" step="1" required></label>
          <label>响应上限（KiB）<input v-model.number="form.bodyKiB" type="number" min="0.0009765625" max="1024" step="any" required></label>
          <label>内容断言<select v-model="form.assertion" :disabled="form.method === 'HEAD'"><option value="none">不检查内容</option><option value="text_contains">包含文本</option><option value="json_equals">JSON 字段相等</option></select></label>
          <label v-if="form.method === 'GET' && form.assertion === 'text_contains'">必须包含的文本<input v-model="form.text" required maxlength="4096"></label>
          <template v-if="form.method === 'GET' && form.assertion === 'json_equals'"><label>对象键路径（JSON 数组）<input v-model="form.jsonPath" required placeholder='["data", "status"]'></label><label>预期 JSON 值<input v-model="form.expected" required placeholder='"ok"'></label></template>
        </div>
        <p>默认仅允许公网目标与 80/443 端口。内网网段和额外端口需在 Agent 本地配置，面板不能放宽该限制。HEAD 不检查内容。</p>
        <fieldset class="assignment-box"><legend>观测节点（1–100 个）</legend><label v-for="node in nodes" :key="node.id" class="check-chip"><input v-model="form.nodeIDs" type="checkbox" :value="node.id">{{ node.name }}</label><p v-if="!nodes.length">请先添加节点。</p><p>{{ form.nodeIDs.length }} 个已选节点</p></fieldset>
        <label class="switch-row"><input v-model="form.enabled" type="checkbox">启用此服务配置</label>
        <div class="form-actions"><button class="primary-button" :disabled="!form.nodeIDs.length || form.nodeIDs.length > 100">保存服务</button><button type="button" @click="editing = false">取消编辑</button></div>
      </fieldset>
    </form>
    <div class="form-actions"><button :disabled="busy" @click="run(() => refresh())">刷新服务列表</button><span v-if="busy" role="status">正在处理…</span></div>
    <p v-if="loaded && !items.length" class="admin-panel">暂无 HTTP 服务。新建服务后可选择观测节点与检查条件。</p>
    <div class="admin-list"><article v-for="item in items" :key="item.id" class="admin-panel entity-card"><div class="entity-title"><strong>{{ item.name }}</strong><span>{{ item.enabled ? '配置已启用' : '配置已停用' }}</span></div><div class="entity-meta"><span>{{ item.node_count }} 个观测节点</span><span>每 {{ item.interval_seconds }} 秒</span><span>配置版本 {{ item.revision }}</span></div><p v-if="!item.node_count">未分配节点，无法执行检查。</p><div class="entity-actions"><button :disabled="busy" @click="edit(item.id)">编辑服务</button><button class="danger-link" :disabled="busy" @click="deleting = item">删除服务</button></div></article></div>
    <button v-if="cursor" :disabled="busy" @click="run(() => refresh(true))">加载更多服务</button>
    <DsConfirmDialog :open="!!deleting" title="删除 HTTP 服务" :description="`删除「${deleting?.name || ''}」及其节点关联、任务和历史数据？此操作不可撤销。`" confirm-label="删除服务和历史" danger :busy="busy" @cancel="!busy && (deleting = null)" @confirm="remove" />
  </section>
</template>

<style scoped>
.service-fields { border: 0; padding: 0; margin: 0; min-width: 0; }
.service-monitoring { display: grid; gap: 20px; min-width: 0; }
.service-monitoring p { overflow-wrap: anywhere; }
.assignment-box { min-width: 0; }
.service-monitoring .entity-title { flex-wrap: wrap; gap: 12px; }
.service-monitoring .entity-title strong { overflow-wrap: anywhere; min-width: 0; }
</style>
