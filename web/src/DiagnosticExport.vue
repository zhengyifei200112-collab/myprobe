<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { AdminAPIError, downloadDiagnosticExport, prepareDiagnosticExport } from './admin-api'
import type { DiagnosticPreview } from './diagnostic-api'

const emit = defineEmits<{ unauthorized: [] }>()
const preview = ref<DiagnosticPreview | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const clock = ref(Date.now())
const expired = computed(() => Boolean(preview.value && clock.value >= Date.parse(preview.value.preview.expires_at)))
const included: Record<string, string> = { normalized_versions: '归一化的版本与构建类型', configuration_structure: '配置结构与保留时长', migration_and_database_sizes: '迁移编号与数据库容量', configuration_counts: '节点、渠道等数量和版本分布', active_queue_counts: '通知活动队列数量', job_and_worker_state: '任务状态与固定错误分类', transport_counts: '连接与待回报数量' }
const excluded: Record<string, string> = { credentials_and_tokens: '密码、凭据与令牌', database_contents: '完整数据库与业务记录', backup_keys_and_paths: '备份密钥与文件路径', addresses_and_target_urls: '地址与目标 URL', names_notes_and_hostnames: '名称、备注与主机名', payloads_sessions_and_audit_contents: '通知正文、会话与审计内容', arbitrary_version_labels: '自定义版本标签原文' }
const messages: Record<string, string> = { diagnostic_preview_missing: '预览已过期、服务已重启或登录会话已变化，请重新生成。', diagnostic_preview_capacity: '待下载预览已达到数量上限，请稍后重试。', diagnostic_collection_unavailable: '诊断信息采集超时或不可用，请重试。', diagnostic_audit_unavailable: '审计记录暂时无法保存，文件未释放，请稍后重试。', diagnostic_download_mismatch: '收到的文件与预览的大小或校验和不一致，请重新生成。' }
let controller: AbortController | undefined
let alive = true
const timer = setInterval(() => { clock.value = Date.now() }, 1000)
function fail(reason: unknown) {
  if (reason instanceof AdminAPIError && (reason.status === 401 || reason.status === 403)) { preview.value = null; emit('unauthorized'); return }
  const code = reason instanceof Error ? reason.message : ''
  error.value = messages[code] || '诊断导出失败，请检查登录状态后重试。'
  if (reason instanceof AdminAPIError && reason.status === 404) preview.value = null
}
async function prepare() {
  busy.value = true; error.value = ''; notice.value = ''; preview.value = null
  controller?.abort(); controller = new AbortController()
  const requestController = controller, timeout = setTimeout(() => requestController.abort(), 12000)
  try {
    const result = await prepareDiagnosticExport(controller.signal)
    if (!alive) return
    if (result.bundle.format !== 'myprobe.diagnostics.v1') throw new Error('unsupported_diagnostic_format')
    preview.value = result; clock.value = Date.now()
  } catch (reason) { if (alive) fail(reason) }
  finally { clearTimeout(timeout); busy.value = false }
}
async function download() {
  if (!preview.value || expired.value) return
  busy.value = true; error.value = ''; notice.value = ''
  controller?.abort(); controller = new AbortController()
  const requestController = controller, timeout = setTimeout(() => requestController.abort(), 12000)
  const selected = preview.value
  try {
    const blob = await downloadDiagnosticExport(selected.preview, controller.signal)
    if (!alive) return
    const url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = selected.preview.file_name; document.body.appendChild(link); link.click(); link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
    notice.value = '诊断文件已交给浏览器下载，响应准备已记录审计。'
  } catch (reason) { if (alive) fail(reason) }
  finally { clearTimeout(timeout); busy.value = false }
}
onUnmounted(() => { alive = false; clearInterval(timer); controller?.abort(); preview.value = null })
</script>

<template>
  <section class="admin-panel diagnostic-export" aria-labelledby="diagnostic-export-title" :aria-busy="busy">
    <div class="export-heading"><div><span class="eyebrow">DIAGNOSTIC EXPORT</span><h2 id="diagnostic-export-title">导出脱敏诊断</h2></div><button class="soft-button" :disabled="busy" @click="prepare">{{ preview ? '重新生成预览' : '生成诊断预览' }}</button></div>
    <p>先检查本次内容，再下载固定快照。预览仅在当前登录会话中有效，5 分钟后或服务重启后须重新生成。</p>
    <p v-if="busy" role="status">正在准备诊断…</p><p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <template v-if="preview">
      <div class="export-scope"><div><h3>包含范围</h3><ul><li v-for="item in preview.bundle.included" :key="item">{{ included[item] || '未识别的范围' }}</li></ul></div><div><h3>排除范围</h3><ul><li v-for="item in preview.bundle.excluded" :key="item">{{ excluded[item] || '未识别的范围' }}</li></ul></div></div>
      <dl><dt>快照时间</dt><dd>{{ new Date(preview.bundle.generated_at).toLocaleString() }}</dd><dt>预览到期</dt><dd>{{ new Date(preview.preview.expires_at).toLocaleString() }}</dd><dt>文件大小</dt><dd>{{ preview.preview.bytes.toLocaleString() }} 字节</dd><dt>SHA-256</dt><dd class="export-hash">{{ preview.preview.sha256 }}</dd></dl>
      <p>计数、任务和连接分别采样，不能当作跨系统同一时刻的快照；不可用字段保留未知含义。Agent 版本来自已保存的握手，可能早于当前连接；最多展示 32 种，其他记录单独计数。版本只保留主、次、补丁号与构建类型，不保留自定义标签原文。</p>
      <details><summary>查看本次文件内容</summary><pre>{{ JSON.stringify(preview.bundle, null, 2) }}</pre></details>
      <p v-if="expired" role="status">预览已过期，请重新生成后下载。</p>
      <button class="primary-button" :disabled="busy || expired" @click="download">下载已预览的 JSON</button>
    </template>
  </section>
</template>

<style scoped>
.diagnostic-export { padding: 20px; min-width: 0; }.export-heading { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 12px; }.export-heading h2 { margin: 6px 0; }
.diagnostic-export p,.diagnostic-export li { color: var(--muted); line-height: 1.7; overflow-wrap: anywhere; }.export-scope { display: grid; grid-template-columns: repeat(auto-fit,minmax(min(100%,230px),1fr)); gap: 16px; }.export-scope h3 { font-size: 15px; }
dl { display: grid; grid-template-columns: minmax(70px,auto) minmax(0,1fr); gap: 10px; font-size: 14px; }dt { color: var(--muted); }dd { margin: 0; overflow-wrap: anywhere; }.export-hash { font-family: monospace; }
details { margin: 18px 0; }summary { cursor: pointer; }pre { max-height: 400px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; padding: 14px; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; font-size: 12px; }
</style>
