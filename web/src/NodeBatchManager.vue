<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { NodeMetadata } from './types'
import { AdminRequestError, applyNodeBatch, previewNodeBatch, type AdminTarget, type NodeBatchPreview, type NodeBatchRequest, type NodeBatchResult } from './admin-api'

const props = defineProps<{ nodes: NodeMetadata[]; targets: AdminTarget[] }>()
const emit = defineEmits<{ applied: [] }>()
const selected = ref<string[]>([])
const search = ref('')
const page = ref(1)
const kind = ref('tags')
const addTags = ref('')
const removeTags = ref('')
const hidden = ref(true)
const changeCollection = ref(true)
const changeReport = ref(false)
const collection = ref(5)
const report = ref(5)
const targetMode = ref<'add' | 'remove' | 'replace'>('add')
const targetIDs = ref<string[]>([])
const busy = ref(false)
const error = ref('')
const conflicts = ref<string[]>([])
const preview = ref<NodeBatchPreview>()
const result = ref<NodeBatchResult>()
const key = ref('')
const matches = computed(() => props.nodes.filter(node => [node.name, ...(node.tags || [])].some(value => value.toLowerCase().includes(search.value.trim().toLowerCase()))))
const pages = computed(() => Math.max(1, Math.ceil(matches.value.length / 20)))
const visible = computed(() => matches.value.slice((page.value - 1) * 20, page.value * 20))
const names = computed(() => new Map(props.nodes.map(node => [node.id, node.name])))
watch(search, () => { page.value = 1 })
watch(pages, value => { page.value = Math.min(page.value, value) })
watch([selected, kind, addTags, removeTags, hidden, changeCollection, changeReport, collection, report, targetMode, targetIDs], () => {
  preview.value = undefined; result.value = undefined; key.value = ''; conflicts.value = []; error.value = ''
}, { deep: true })
function selectPage() {
  const next = [...new Set([...selected.value, ...visible.value.map(node => node.id)])]
  if (next.length > 100) { error.value = '每批最多选择 100 个节点，请拆分执行。'; return }
  selected.value = next
}
function selectNode(id: string, checked: boolean) {
  if (checked && selected.value.length >= 100) { error.value = '每批最多选择 100 个节点。'; return }
  selected.value = checked ? [...selected.value, id] : selected.value.filter(value => value !== id)
}
const tags = (text: string) => [...new Set(text.split(/[,，]/).map(value => value.trim()).filter(Boolean))]
function request(): NodeBatchRequest {
  const value: NodeBatchRequest = { node_ids: selected.value }
  if (kind.value === 'tags') { value.add_tags = tags(addTags.value); value.remove_tags = tags(removeTags.value) }
  if (kind.value === 'visibility') value.hidden = hidden.value
  if (kind.value === 'intervals') { if (changeCollection.value) value.collection_seconds = collection.value; if (changeReport.value) value.report_seconds = report.value }
  if (kind.value === 'targets') value.targets = { mode: targetMode.value, ids: targetIDs.value }
  return value
}
function showError(value: unknown) {
  error.value = value instanceof Error ? value.message : '操作失败'
  if (value instanceof AdminRequestError && value.status === 409) {
    conflicts.value = value.conflictNodeIDs
    preview.value = undefined; key.value = ''
    error.value = '配置已变化、预览已过期或请求键冲突，请重新预览整批变更。'
  }
}
async function prepare() {
  busy.value = true; error.value = ''; result.value = undefined; conflicts.value = []
  try { preview.value = await previewNodeBatch(request()); key.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('') }
  catch (value) { showError(value) } finally { busy.value = false }
}
async function apply() {
  if (!preview.value) return
  busy.value = true; error.value = ''
  try {
    result.value = await applyNodeBatch(preview.value.id, key.value)
    preview.value = undefined
    emit('applied')
  } catch (value) { showError(value) } finally { busy.value = false }
}
function summary(value: NodeBatchPreview['nodes'][number]['before']) {
  return `标签：${value.tags.join('、') || '无'}；${value.hidden ? '隐藏' : '公开'}；采集 ${value.collection_seconds}s / 上报 ${value.report_seconds}s；目标：${value.target_ids.map(id => props.targets.find(target => target.id === id)?.name || id).join('、') || '无'}`
}
</script>

<template>
  <details class="batch-manager admin-panel">
    <summary>批量节点管理 <span>预览后整批执行 · 每批最多 100 个节点</span></summary>
    <form @submit.prevent="prepare">
      <fieldset :disabled="busy">
        <legend>1. 选择节点</legend>
        <label>搜索批量节点<input v-model="search" type="search" placeholder="名称或标签"></label>
        <div class="batch-toolbar"><button type="button" @click="selectPage">选择本页</button><button type="button" @click="selected = []">清空选择</button><strong role="status">已选 {{ selected.length }} 个（跨页保留）</strong></div>
        <div class="batch-node-options"><label v-for="node in visible" :key="node.id" :class="{ conflict: conflicts.includes(node.id) }"><input type="checkbox" :checked="selected.includes(node.id)" :disabled="!selected.includes(node.id) && selected.length >= 100" @change="selectNode(node.id, ($event.target as HTMLInputElement).checked)">{{ node.name }}</label></div>
        <p v-if="!visible.length">没有匹配的节点。</p>
        <div class="batch-toolbar"><button type="button" :disabled="page <= 1" @click="page--">上一页</button><span>第 {{ page }} / {{ pages }} 页</span><button type="button" :disabled="page >= pages" @click="page++">下一页</button></div>
      </fieldset>
      <fieldset :disabled="busy">
        <legend>2. 配置变更</legend>
        <label>批量操作类型<select v-model="kind" aria-label="批量操作类型"><option value="tags">增删标签</option><option value="visibility">公开可见性</option><option value="intervals">采集 / 上报间隔</option><option value="targets">探测目标分配</option></select></label>
        <div v-if="kind === 'tags'" class="batch-fields"><label>添加标签<input v-model="addTags" placeholder="逗号分隔"></label><label>移除标签<input v-model="removeTags" placeholder="逗号分隔"></label></div>
        <label v-else-if="kind === 'visibility'">设置可见性<select v-model="hidden" aria-label="设置可见性"><option :value="true">从公开面板隐藏</option><option :value="false">公开展示</option></select></label>
        <div v-else-if="kind === 'intervals'" class="batch-fields"><label><span><input v-model="changeCollection" type="checkbox">修改采集间隔</span><input v-model.number="collection" aria-label="批量采集间隔（秒）" type="number" min="1" max="3600" :disabled="!changeCollection" required></label><label><span><input v-model="changeReport" type="checkbox">修改上报间隔</span><input v-model.number="report" aria-label="批量上报间隔（秒）" type="number" min="1" max="3600" :disabled="!changeReport" required></label></div>
        <div v-else><label>目标分配方式<select v-model="targetMode" aria-label="目标分配方式"><option value="add">添加（保留已有目标）</option><option value="remove">移除所选目标</option><option value="replace">替换全部目标</option></select></label><p v-if="targetMode === 'replace'">将覆盖每个节点的原有目标；不选目标表示清空分配。</p><div class="batch-node-options"><label v-for="target in targets" :key="target.id"><input v-model="targetIDs" type="checkbox" :value="target.id">{{ target.name }}</label></div></div>
        <button type="submit" :disabled="!selected.length">{{ busy ? '处理中…' : '预览批量变更' }}</button>
      </fieldset>
    </form>
    <p v-if="error" role="alert" class="batch-error">{{ error }}</p>
    <p v-if="conflicts.length" class="batch-error">冲突节点：{{ conflicts.map(id => names.get(id) || id).join('、') }}</p>
    <section v-if="preview" class="batch-preview" aria-label="批量变更预览">
      <h3>3. 确认预览</h3><p>{{ preview.nodes.length }} 个节点；预览有效至 {{ new Date(preview.expires_at).toLocaleString() }}。修改选择或操作会清除预览。</p>
      <ol><li v-for="item in preview.nodes" :key="item.node_id"><strong>{{ item.name }} · {{ item.changed ? '将修改' : '无变化' }}</strong><p>修改前：{{ summary(item.before) }}</p><p v-if="item.changed">修改后：{{ summary(item.after) }}</p></li></ol>
      <p>整批成功或整批不变。请求中断时可点击同一按钮重试，不会重复执行。</p>
      <button type="button" :disabled="busy" @click="apply">确认执行 {{ preview.nodes.length }} 个节点</button>
    </section>
    <p v-if="result" role="status">批量执行完成：修改 {{ result.changed_ids.length }} 个，无变化 {{ result.unchanged_ids.length }} 个。</p>
  </details>
</template>

<style scoped>
.batch-manager{margin:0 0 20px}.batch-manager>summary{cursor:pointer;font-weight:600;line-height:1.8}.batch-manager>summary span{font-size:12px;font-weight:400;color:var(--muted);margin-left:12px}.batch-manager fieldset{border:1px solid var(--border);border-radius:12px;margin:18px 0;padding:16px;min-width:0}.batch-manager legend{font-size:14px;font-weight:600}.batch-manager label{display:grid;gap:6px;font-size:12px;min-width:0}.batch-manager input:not([type=checkbox]),.batch-manager select{width:100%;min-width:0}.batch-toolbar{display:flex;flex-wrap:wrap;align-items:center;gap:12px;margin:12px 0;font-size:12px}.batch-node-options{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:10px;margin:16px 0}.batch-node-options label{display:flex;align-items:center;gap:8px;overflow-wrap:anywhere}.batch-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;margin:14px 0}.batch-preview li{padding:12px 0;border-bottom:1px solid var(--border)}.batch-preview p{font-size:12px;overflow-wrap:anywhere;line-height:1.7}.batch-error,.conflict{color:var(--red)}.batch-manager button{min-height:40px;margin-top:8px}.batch-manager input[type=checkbox]{width:16px;height:16px;flex-shrink:0}@media(max-width:599px){.batch-fields{grid-template-columns:1fr}.batch-manager>summary span{display:block;margin-left:0}}
</style>
