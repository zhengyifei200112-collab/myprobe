<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import type { NodeMetadata } from '../types'
import type { MaintenanceDraft, MaintenancePreview, MaintenanceSelection, MaintenanceWindow } from '../admin-api'
import { AdminAPIError, cancelMaintenanceWindow, createMaintenanceWindow, loadMaintenanceWindows, loadNodes, previewMaintenanceWindow } from '../admin-api'
import { DsButton, DsEmptyState } from '../design-system'
import { maintenanceRecoveryKey, readMaintenanceRecovery } from './maintenance-recovery'
import type { PendingMaintenance } from './maintenance-recovery'

const props = defineProps<{ nodes: NodeMetadata[] }>()
const nodeOptions = ref(props.nodes)
watch(() => props.nodes, value => { nodeOptions.value = value })
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
function inputDate(ms: number) {
  const date = new Date(ms)
  return new Date(ms - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}
const form = reactive({ kind: 'nodes' as 'nodes' | 'tags', nodeIDs: [] as string[], tags: '', tagMode: 'all' as 'all' | 'any', reason: '', start: inputDate(Date.now() + 300_000), end: inputDate(Date.now() + 3_900_000) })
const preview = ref<MaintenancePreview | null>(null), requestID = ref('')
const pending = ref<PendingMaintenance | null>(null), recoveryInvalid = ref(false), reviewed = ref(false)
const busy = ref(false), listBusy = ref(false), loaded = ref(false)
const error = ref(''), listError = ref(''), notice = ref('')
const items = ref<MaintenanceWindow[]>([]), cursor = ref(''), cancelTarget = ref<MaintenanceWindow | null>(null)
const clock = ref(Date.now())
const locked = computed(() => busy.value || pending.value !== null || recoveryInvalid.value)
const confirmation = computed(() => pending.value?.kind === 'create' ? pending.value.preview : preview.value)
const names = computed(() => new Map(nodeOptions.value.map(n => [n.id, n.name])))
watch(form, () => { preview.value = null; requestID.value = ''; notice.value = '' }, { deep: true, flush: 'sync' })
function date(value: string, timezone = zone) {
  try { return new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: timezone }) }
  catch { return value }
}
function scope(value?: MaintenanceSelection) {
  if (!value) return '原始选择来源未记录'
  return value.kind === 'nodes' ? '指定节点' : `${value.tag_mode === 'all' ? '包含全部标签' : '包含任意标签'}：${value.tags?.join('、')}`
}
function state(item: MaintenanceWindow) {
  if (item.canceled_at) return '已取消'
  if (Date.parse(item.ends_at) <= clock.value) return '已结束'
  return Date.parse(item.starts_at) <= clock.value ? '进行中' : '即将开始'
}
function describe(e: unknown) {
  if (e instanceof AdminAPIError) {
    const messages: Record<string, string> = { maintenance_preview_changed: '确认内容或成员已变化，请重新预览。', maintenance_request_conflict: '请求身份存在冲突，请先核对窗口及审计记录。', maintenance_conflict: '窗口已被修改或取消，请刷新列表。', maintenance_expired: '窗口已结束，请调整时间并重新预览。', maintenance_limit: '维护窗口容量已达上限。', invalid_maintenance: '请检查时间、原因和选择范围，重新预览。', maintenance_not_found: '窗口已不存在，请核对列表。' }
    return messages[e.code || ''] || e.message
  }
  return e instanceof Error ? e.message : '操作失败，请重试。'
}
async function refresh(more = false) {
  if (listBusy.value) return
  listBusy.value = true; listError.value = ''
  try {
    const [result, inventory] = await Promise.all([loadMaintenanceWindows(more ? cursor.value : ''), more ? Promise.resolve({ nodes: nodeOptions.value }) : loadNodes()])
    nodeOptions.value = inventory.nodes
    items.value = more ? [...new Map([...items.value, ...result.windows].map(w => [w.id, w])).values()] : result.windows
    cursor.value = result.next_cursor; loaded.value = true
  } catch (e) { listError.value = describe(e) }
  finally { listBusy.value = false }
}
async function inspect() {
  if (locked.value) return
  busy.value = true; error.value = ''; notice.value = ''; preview.value = null
  try {
    const selection: MaintenanceSelection = form.kind === 'nodes' ? { kind: 'nodes', node_ids: [...form.nodeIDs] } : { kind: 'tags', tags: form.tags.split('\n').map(t => t.trim()).filter(Boolean), tag_mode: form.tagMode }
    const draft: MaintenanceDraft = { selection, reason: form.reason, timezone: zone, starts_at: new Date(form.start).toISOString(), ends_at: new Date(form.end).toISOString() }
    const result = await previewMaintenanceWindow(draft)
    requestID.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
    preview.value = result
  } catch (e) { error.value = describe(e) }
  finally { busy.value = false }
}
function persist(value: PendingMaintenance) {
  sessionStorage.setItem(maintenanceRecoveryKey, JSON.stringify(value))
  pending.value = value
}
function removeRecovery(value: PendingMaintenance) {
  // A late response from an unmounted tab must not erase a newer operation.
  if (sessionStorage.getItem(maintenanceRecoveryKey) === JSON.stringify(value)) sessionStorage.removeItem(maintenanceRecoveryKey)
}
async function submit(create = false) {
  if (busy.value || recoveryInvalid.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    if (!pending.value) {
      if (create && preview.value && requestID.value) persist({ kind: 'create', preview: preview.value, requestID: requestID.value })
      else if (!create && cancelTarget.value) persist({ kind: 'cancel', windowID: cancelTarget.value.id, revision: cancelTarget.value.revision, reason: cancelTarget.value.reason })
      else return
    }
    const operation = pending.value!
    let message: string
    try {
      if (operation.kind === 'create') {
        const result = await createMaintenanceWindow(operation.preview, operation.requestID)
        message = `${result.replayed ? '已确认此前创建结果' : '维护窗口已创建'}：${result.window_id}`
      } else {
        const result = await cancelMaintenanceWindow(operation.windowID, operation.revision)
        message = `${result.replayed ? '已确认此前取消结果' : '维护窗口已取消'}，取消时间：${date(result.canceled_at)}。`
      }
    } catch (e) {
      // A conflict in identity or a transport/server error may concern an earlier
      // committed request. Keep it; definite validation/conflict rejection unlocks.
      if (e instanceof AdminAPIError && [400, 404, 409].includes(e.status) && e.code !== 'maintenance_request_conflict') {
        removeRecovery(operation); pending.value = null; preview.value = null; requestID.value = ''; cancelTarget.value = null
      }
      throw e
    }
    removeRecovery(operation)
    pending.value = null; preview.value = null; requestID.value = ''; cancelTarget.value = null; reviewed.value = false
    notice.value = message
    await refresh()
  } catch (e) { error.value = describe(e) }
  finally { busy.value = false }
}
function clearRecovery() {
  if (!reviewed.value || busy.value) return
  try {
    sessionStorage.removeItem(maintenanceRecoveryKey)
    pending.value = null; recoveryInvalid.value = false; preview.value = null; requestID.value = ''; cancelTarget.value = null; error.value = ''; reviewed.value = false
  } catch { error.value = '无法清除本地记录，请检查浏览器存储权限。' }
}
let timer: ReturnType<typeof setInterval>
onMounted(() => {
  try {
    const raw = sessionStorage.getItem(maintenanceRecoveryKey)
    if (raw) pending.value = readMaintenanceRecovery(raw)
  } catch { recoveryInvalid.value = true; error.value = '无法读取待确认维护记录。请先核对窗口和审计记录，再清除本地记录。' }
  void refresh()
  timer = setInterval(() => { clock.value = Date.now() }, 1000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <section class="maintenance-manager" aria-label="维护窗口管理">
    <header class="maintenance-heading"><div><h2>维护窗口</h2><p>计划维护期间继续采集和记录故障，暂停通知。窗口结束后以新鲜观测恢复提醒。</p></div><DsButton :disabled="listBusy" @click="refresh()">刷新维护列表</DsButton></header>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="pending || recoveryInvalid" class="maintenance-card recovery" aria-label="待确认维护操作">
      <h3>上次操作结果待确认</h3>
      <p>请使用原请求确认结果。当前标签页刷新后可继续；关闭标签页可能丢失记录。恢复数据库后应先核对恢复时间、窗口与审计。</p>
      <p v-if="pending?.kind==='cancel'">待确认取消：{{ pending.reason }}（{{ pending.windowID }}）</p>
      <DsButton v-if="pending" :disabled="busy" variant="primary" @click="submit()">重试确认维护结果</DsButton>
      <details><summary>核对结果后处理本地记录</summary><p>清除记录不会撤销服务端操作，也不能证明原请求失败。</p><label class="check"><input v-model="reviewed" type="checkbox" :disabled="busy">我已核对窗口和审计记录</label><DsButton :disabled="!reviewed || busy" @click="clearRecovery">清除本地待确认记录</DsButton></details>
    </section>
    <form class="maintenance-card" @submit.prevent="inspect">
      <h3>新建一次性维护</h3>
      <fieldset :disabled="locked"><legend>时间和范围</legend>
        <div class="maintenance-fields"><label>开始时间<input v-model="form.start" type="datetime-local" required></label><label>结束时间<input v-model="form.end" type="datetime-local" required></label></div>
        <p>时间按浏览器当前时区 {{ zone }} 输入，保存为 UTC。结束必须晚于开始及当前时间。</p>
        <label>维护原因<input v-model="form.reason" required maxlength="1000" placeholder="例如：升级系统"></label>
        <label>选择范围<select v-model="form.kind" aria-label="选择范围"><option value="nodes">指定节点</option><option value="tags">按标签选择</option></select></label>
        <label v-if="form.kind==='nodes'">维护节点（可多选）<select v-model="form.nodeIDs" aria-label="维护节点（可多选）" multiple size="5"><option v-for="node in nodeOptions" :key="node.id" :value="node.id">{{ node.name }}</option></select></label>
        <template v-else><label>标签（每行一个）<textarea v-model="form.tags" rows="3" required></textarea></label><label>标签匹配方式<select v-model="form.tagMode" aria-label="标签匹配方式"><option value="all">包含全部标签</option><option value="any">包含任意标签</option></select></label></template>
      </fieldset>
      <DsButton type="submit" :disabled="locked">预览维护窗口</DsButton>
    </form>
    <section v-if="confirmation" class="maintenance-card" aria-label="维护窗口预览">
      <h3>确认维护窗口 · {{ confirmation.members.length }} 个节点</h3>
      <dl><div><dt>原因</dt><dd>{{ confirmation.draft.reason }}</dd></div><div><dt>开始 / 结束</dt><dd>{{ date(confirmation.draft.starts_at,confirmation.draft.timezone) }} → {{ date(confirmation.draft.ends_at,confirmation.draft.timezone) }}（{{ confirmation.draft.timezone }}）</dd></div><div><dt>选择来源</dt><dd>{{ scope(confirmation.draft.selection) }}</dd></div></dl>
      <ul><li v-for="member in confirmation.members" :key="member.id">{{ member.name }}</li></ul>
      <p>创建时冻结这些成员；后来匹配标签的新节点不会加入。重叠窗口中，只要仍有有效维护就继续抑制通知。已开始的外部发送可能无法撤回。</p>
      <DsButton v-if="!pending" variant="primary" :disabled="busy || recoveryInvalid" @click="submit(true)">确认创建维护窗口</DsButton>
    </section>
    <section v-if="cancelTarget && !pending" class="maintenance-card" aria-label="取消维护确认">
      <h3>取消维护：{{ cancelTarget.reason }}</h3><p>取消后保留历史。其他重叠窗口继续有效，故障恢复提醒需要新鲜观测。</p><DsButton variant="danger" :disabled="locked" @click="submit()">确认取消维护窗口</DsButton> <DsButton :disabled="locked" @click="cancelTarget=null">返回列表</DsButton>
    </section>
    <p v-if="listError" role="alert">{{ listError }}</p>
    <p v-if="listBusy" role="status">正在加载维护窗口…</p>
    <p>列表按窗口 ID 分页。最多保留 1000 个窗口，每个窗口最多 100 个成员。</p>
    <div class="maintenance-list">
      <article v-for="item in items" :key="item.id" class="maintenance-card" :aria-label="`维护：${item.reason}`">
        <header><h3>{{ item.reason }}</h3><span class="maintenance-state">{{ state(item) }}</span></header>
        <p>{{ date(item.starts_at,item.timezone) }} → {{ date(item.ends_at,item.timezone) }}（{{ item.timezone }}）</p>
        <p v-if="item.canceled_at">取消时间：{{ date(item.canceled_at,item.timezone) }} · 操作者：{{ item.canceled_by || '未记录' }}</p>
        <p>{{ scope(item.selection) }} · 冻结 {{ item.node_ids.length }} 个成员</p>
        <details><summary>查看冻结成员与记录</summary><ul><li v-for="id in item.node_ids" :key="id">{{ names.get(id) || `已删除节点（${id}）` }}</li></ul><p>窗口 ID：{{ item.id }} · 版本 {{ item.revision }} · 创建人 {{ item.created_by }} · 创建时间 {{ date(item.created_at,item.timezone) }}</p></details>
        <DsButton v-if="state(item)==='进行中' || state(item)==='即将开始'" :disabled="locked" @click="cancelTarget=item">取消此维护</DsButton>
      </article>
    </div>
    <DsEmptyState v-if="loaded && !items.length" title="尚无维护窗口" description="创建一次性维护，暂停计划工作期间的通知。" />
    <DsButton v-if="cursor" :disabled="listBusy" @click="refresh(true)">加载更多维护窗口</DsButton>
  </section>
</template>

<style scoped>
.maintenance-manager { display:grid; gap:20px; min-width:0; }
.maintenance-heading,.maintenance-card header { display:flex; flex-wrap:wrap; gap:12px; justify-content:space-between; align-items:start; }
.maintenance-heading h2,.maintenance-card h3 { margin:0; }
.maintenance-heading p { margin-bottom:0; }
.maintenance-card { display:grid; gap:12px; min-width:0; padding:20px; border:1px solid var(--border); border-radius:16px; background:var(--surface); }
.maintenance-card p { margin:0; color:var(--text-secondary); }
.maintenance-card fieldset { display:grid; gap:14px; margin:0; padding:0; border:0; min-width:0; }
.maintenance-card legend { margin-bottom:12px; font-weight:600; }
.maintenance-card label { display:grid; gap:6px; min-width:0; }
.maintenance-fields { display:grid; grid-template-columns:repeat(auto-fit,minmax(210px,1fr)); gap:14px; }
.maintenance-card input,.maintenance-card select,.maintenance-card textarea { width:100%; box-sizing:border-box; min-width:0; padding:10px 12px; border:1px solid var(--border); border-radius:10px; background:var(--surface-subtle); color:inherit; font:inherit; }
.maintenance-card input:focus-visible,.maintenance-card select:focus-visible,.maintenance-card textarea:focus-visible,.maintenance-card summary:focus-visible { outline:2px solid var(--blue); outline-offset:2px; }
.maintenance-card .check { display:flex; align-items:center; gap:8px; margin:12px 0; }
.maintenance-card .check input { width:auto; }
.maintenance-card summary { cursor:pointer; }
.maintenance-card ul { margin:4px 0; padding-left:20px; max-height:240px; overflow:auto; }
.maintenance-list { display:grid; gap:12px; }
.maintenance-card dl { margin:0; display:grid; gap:10px; }
.maintenance-card dt { color:var(--text-secondary); }
.maintenance-card dd { margin:4px 0 0; }
.maintenance-state { border:1px solid var(--border); padding:3px 10px; border-radius:20px; font-size:13px; }
.maintenance-manager p,.maintenance-manager li,.maintenance-manager h3,.maintenance-manager dd { overflow-wrap:anywhere; }
.maintenance-card .ds-button { justify-self:start; }
@media(max-width:480px) { .maintenance-card { padding:14px; } .maintenance-fields { grid-template-columns:minmax(0,1fr); } }
</style>
