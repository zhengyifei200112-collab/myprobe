<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import type { NodeMetadata } from '../types'
import type { AlertKind, AlertPolicy, AlertPolicyDecision, AlertPolicyInput, AlertPolicyScope, NotificationChannel, PolicyEvaluationStatus } from '../admin-api'
import { createAlertPolicy, deleteAlertPolicy, loadAlertPolicies, loadAlertPolicy, loadEffectiveAlertPolicies, updateAlertPolicy } from '../admin-api'
import { DsConfirmDialog } from '../design-system'
import PolicyTransfer from './PolicyTransfer.vue'
const props = defineProps<{ nodes: NodeMetadata[]; channels: NotificationChannel[]; initialPolicyId?: string }>()
const items = ref<AlertPolicy[]>([]), cursor = ref(''), busy = ref(false), error = ref(''), loaded = ref(false), execution = ref(false)
const editing = ref(false), deleting = ref<AlertPolicy | null>(null), nodeID = ref(''), decisions = ref<AlertPolicyDecision[] | null>(null)
const names = ref<Record<string, string>>({})
const evaluation = ref<PolicyEvaluationStatus>({ state: 'pending' })
let active = true
onUnmounted(() => { active = false })
const defaults = () => ({ id: '', revision: 0, name: '', key: '', enabled: true, priority: 0, scope: 'all' as AlertPolicyScope['kind'], nodeIDs: [] as string[], tags: '', tagMode: 'all' as 'all' | 'any', channel: '', kind: 'cpu' as AlertKind, threshold: 90, duration: 0, recovery: 0, recoveryMatches: true, repeat: 900, cooldown: 900, template: '' })
const form = reactive(defaults())
const kinds: Record<AlertKind, string> = { cpu: 'CPU', memory: '内存', disk: '磁盘', offline: '离线', latency: '延迟', bandwidth: '带宽', cycle_traffic: '周期流量', expiry: '到期' }
const fields = { cpu: 'threshold_percent', memory: 'threshold_percent', disk: 'threshold_percent', offline: 'offline_seconds', latency: 'threshold_milliseconds', bandwidth: 'threshold_bytes_per_second', cycle_traffic: 'threshold_bytes', expiry: 'days_before' } as const
const thresholdLabel = computed(() => ({ cpu: '阈值（%）', memory: '阈值（%）', disk: '阈值（%）', offline: '离线阈值（秒）', latency: '延迟阈值（毫秒）', bandwidth: '带宽阈值（字节/秒）', cycle_traffic: '流量阈值（字节）', expiry: '提前提醒（天）' }[form.kind]))
async function run(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await action() } catch (e) { if (active) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { if (active) busy.value = false }
}
async function refresh(more = false) {
  const response = await loadAlertPolicies(more ? cursor.value : '')
  if (!active) return
  items.value = more ? [...items.value, ...response.policies.filter(p => !items.value.some(old => old.id === p.id))] : response.policies
  cursor.value = response.next_cursor; execution.value = response.evaluation_enabled; loaded.value = true
  evaluation.value = response.evaluation
}
function create() { Object.assign(form, defaults()); editing.value = true; error.value = '' }
async function edit(id: string) {
  await run(async () => {
    const { policy: p } = await loadAlertPolicy(id)
    if (!active) return
    Object.assign(form, defaults(), { id: p.id, revision: p.revision, name: p.name, key: p.policy_key, enabled: p.enabled, priority: p.priority, scope: p.scope.kind, nodeIDs: p.scope.kind === 'nodes' ? [...p.scope.node_ids] : [], tags: p.scope.kind === 'tags' ? p.scope.tags.join('\n') : '', tagMode: p.scope.kind === 'tags' ? p.scope.tag_mode : 'all', channel: p.channel_id, kind: p.kind, threshold: p.config[fields[p.kind]] ?? 0, duration: p.config.duration_seconds ?? 0, recovery: p.config.recovery_seconds ?? 0, recoveryMatches: p.config.recovery_seconds == null, repeat: p.config.repeat_seconds ?? 0, cooldown: p.cooldown_seconds, template: p.config.template_id ?? '' })
    editing.value = true
  })
}
async function save() {
  await run(async () => {
    if (!Number.isFinite(form.threshold) || form.threshold < 0 || (['bandwidth', 'cycle_traffic'].includes(form.kind) && !Number.isSafeInteger(form.threshold))) throw new Error('阈值无效；字节值须为可精确表示的整数。')
    const scope: AlertPolicyScope = form.scope === 'all' ? { kind: 'all' } : form.scope === 'nodes' ? { kind: 'nodes', node_ids: [...form.nodeIDs] } : { kind: 'tags', tag_mode: form.tagMode, tags: form.tags.split('\n').map(t => t.trim()).filter(Boolean) }
    const payload: AlertPolicyInput = { name: form.name, policy_key: form.key, enabled: form.enabled, priority: form.priority, scope, channel_id: form.channel, kind: form.kind, config: { [fields[form.kind]]: form.threshold, duration_seconds: form.duration, ...(form.recoveryMatches ? {} : { recovery_seconds: form.recovery }), repeat_seconds: form.repeat, ...(form.template ? { template_id: form.template } : {}) }, cooldown_seconds: form.cooldown }
    if (form.id) await updateAlertPolicy(form.id, { ...payload, revision: form.revision }); else await createAlertPolicy(payload)
    if (!active) return
    editing.value = false; decisions.value = null
    await refresh()
  })
}
async function remove() {
  const p = deleting.value; if (!p) return
  await run(async () => { await deleteAlertPolicy(p.id, p.revision); if (!active) return; deleting.value = null; decisions.value = null; await refresh() })
}
async function preview() {
  if (!nodeID.value) return
  decisions.value = null
  await run(async () => {
    const response = await loadEffectiveAlertPolicies(nodeID.value)
    const ids = [...new Set(response.decisions.flatMap(d => d.candidate_ids))]
    const pairs: Array<readonly [string, string]> = []
    for (let offset = 0; offset < ids.length; offset += 8) {
      if (!active) return
      pairs.push(...await Promise.all(ids.slice(offset, offset + 8).map(async id => { const { policy } = await loadAlertPolicy(id); return [id, policy.name] as const })))
    }
    if (!active) return
    names.value = Object.fromEntries(pairs); decisions.value = response.decisions; execution.value = response.evaluation_enabled
    evaluation.value = response.evaluation
  })
}
const reasons = { only_match: '唯一匹配', more_specific_scope: '范围更具体', higher_priority: '同层级优先级更高' }
onMounted(async () => {
  await run(() => refresh())
  if (active && props.initialPolicyId) await edit(props.initialPolicyId)
})
</script>

<template>
  <section class="policy-manager" aria-label="范围策略管理">
    <h2>范围策略</h2>
    <PolicyTransfer :nodes="nodes" :channels="channels" @imported="run(() => refresh())" />
    <p v-if="loaded && !execution" role="status">策略评估尚未启用。</p>
    <div v-else-if="loaded" role="status">
      <p v-if="evaluation.state === 'pending'">等待首次应用策略。</p>
      <p v-else-if="evaluation.state === 'error'">最近一次策略应用失败，请检查服务端日志。新配置尚不能确认为已生效。</p>
      <p v-else>策略评估已运行。</p>
      <p v-if="evaluation.last_applied_at">最近成功应用：{{ new Date(evaluation.last_applied_at).toLocaleString() }}</p>
      <p>配置在下一轮评估时应用，通常间隔 15 秒。保存和命中预览不代表已应用，也不代表通知已送达；刷新可查看最新状态。</p>
    </div>
    <p>同一策略键按“显式节点 → 标签 → 全局”覆盖，同层级优先级越大越优先。不同策略键独立生效。</p>
    <p v-if="error" role="alert" class="form-message error">{{ error }}</p>
    <div class="form-actions"><button :disabled="busy" @click="create">新建范围策略</button><button :disabled="busy" @click="run(() => refresh())">刷新策略</button></div>
    <form v-if="editing" class="admin-panel" @submit.prevent="save">
      <h3>{{ form.id ? '编辑范围策略' : '创建范围策略' }}</h3>
      <fieldset :disabled="busy" class="policy-fields">
        <label>策略名称<input v-model="form.name" required maxlength="128"></label>
        <label>策略键<input v-model="form.key" required pattern="[a-zA-Z0-9][a-zA-Z0-9_.:\-]*" maxlength="128"><small>例如 cpu.warning；相同键用于覆盖。</small></label>
        <label>优先级<input v-model.number="form.priority" type="number" min="-1000" max="1000" step="1" required></label>
        <label>匹配范围<select v-model="form.scope"><option value="all">全部节点</option><option value="nodes">指定节点</option><option value="tags">按标签动态匹配</option></select></label>
        <div v-if="form.scope === 'nodes'"><p>选择节点（最多 100 个）</p><label v-for="n in nodes" :key="n.id" class="check"><input v-model="form.nodeIDs" type="checkbox" :value="n.id">{{ n.name }}</label></div>
        <template v-if="form.scope === 'tags'"><label>标签（每行一个）<textarea v-model="form.tags" required rows="3" /></label><label>标签匹配方式<select v-model="form.tagMode"><option value="all">包含全部标签</option><option value="any">包含任意标签</option></select></label></template>
        <label>通知通道<select v-model="form.channel" required><option value="" disabled>选择通道</option><option v-for="c in channels" :key="c.id" :value="c.id">{{ c.name }}{{ c.enabled ? '' : '（已停用）' }}</option></select></label>
        <label>告警类型<select v-model="form.kind"><option v-for="(label, kind) in kinds" :key="kind" :value="kind">{{ label }}</option></select></label>
        <label>{{ thresholdLabel }}<input v-model.number="form.threshold" type="number" min="0" step="any" required></label>
        <label>持续时间（秒）<input v-model.number="form.duration" type="number" min="0" max="2592000" step="1" required></label>
        <label class="check"><input v-model="form.recoveryMatches" type="checkbox">恢复持续时间与触发一致</label>
        <label v-if="!form.recoveryMatches">恢复持续时间（秒）<input v-model.number="form.recovery" type="number" min="0" max="2592000" step="1" required></label>
        <label>重复提醒（秒，0 使用冷却间隔）<input v-model.number="form.repeat" type="number" min="0" max="2592000" step="1" required></label>
        <label>冷却间隔（秒）<input v-model.number="form.cooldown" type="number" min="30" max="2592000" step="1" required></label>
        <label class="check"><input v-model="form.enabled" type="checkbox">启用此策略配置</label>
        <div class="form-actions"><button type="submit">保存范围策略</button><button type="button" @click="editing = false">取消编辑</button></div>
      </fieldset>
    </form>
    <article v-for="p in items" :key="p.id" class="admin-panel"><h3>{{ p.name }}</h3><p>{{ p.policy_key }} · {{ { all: '全部节点', nodes: '指定节点', tags: '动态标签' }[p.scope.kind] }} · 优先级 {{ p.priority }} · 版本 {{ p.revision }} · {{ p.enabled ? '配置已启用' : '配置已停用' }}</p><div class="form-actions"><button :disabled="busy" @click="edit(p.id)">编辑策略</button><button :disabled="busy" @click="deleting = p">删除策略</button></div></article>
    <p v-if="loaded && !items.length">暂无范围策略。</p><button v-if="cursor" :disabled="busy" @click="run(() => refresh(true))">加载更多策略</button>
    <section class="admin-panel" aria-label="策略继承预览"><h3>按节点查看继承结果</h3><label>预览节点<select v-model="nodeID" :disabled="busy" @change="decisions = null"><option value="" disabled>选择节点</option><option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}</option></select></label><button :disabled="busy || !nodeID" @click="preview">查询继承结果</button><p v-if="decisions && !decisions.length">没有匹配的已启用策略。</p><article v-for="d in decisions" :key="d.policy_key"><h4>{{ d.policy_key }}</h4><p>选中 {{ names[d.selected_id] }} · {{ reasons[d.reason] }}</p><ol><li v-for="id in d.candidate_ids" :key="id">{{ names[id] }}{{ id === d.selected_id ? '（选中）' : '（被覆盖）' }}</li></ol></article></section>
    <DsConfirmDialog :open="!!deleting" title="删除范围策略" :description="`确认删除「${deleting?.name || ''}」？删除后无法撤销。`" confirm-label="确认删除策略" danger :busy="busy" @cancel="!busy && (deleting = null)" @confirm="remove" />
  </section>
</template>

<style scoped>
.policy-manager { display: grid; gap: 16px; min-width: 0; }
.policy-manager .admin-panel { padding: 20px; min-width: 0; }
.policy-manager p, .policy-manager h3, .policy-manager li { overflow-wrap: anywhere; }
.policy-fields { display: grid; grid-template-columns: repeat(auto-fit,minmax(220px,1fr)); gap: 16px; border: 0; padding: 0; min-width: 0; }
.policy-manager label { display: grid; gap: 6px; min-width: 0; }
.policy-manager input, .policy-manager select, .policy-manager textarea { width: 100%; min-width: 0; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-subtle); color: inherit; font: inherit; }
.policy-manager textarea { resize: vertical; }
.policy-manager input:focus-visible, .policy-manager select:focus-visible, .policy-manager textarea:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
.policy-manager .check { display: flex; align-items: center; gap: 8px; }
.policy-manager .check input { width: auto; }
.policy-manager .form-actions { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
