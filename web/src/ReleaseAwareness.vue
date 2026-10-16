<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { AdminAPIError, loadReleaseStatus } from './admin-api'
import type { ReleaseComparison, ReleaseStatus } from './release-api'

const props = defineProps<{ nodeId: string; serverOs?: string }>()
const emit = defineEmits<{ unauthorized: [] }>()
const data = ref<ReleaseStatus | null>(null)
const busy = ref(false)
const error = ref('')
const rejected = ref(false)
const now = ref(Date.now())
let generation = 0
let controller: AbortController | undefined
const timer = window.setInterval(() => { now.value = Date.now() }, 1000)
onUnmounted(() => { generation++; controller?.abort(); clearInterval(timer) })
const sourceStatus = computed(() => {
  const upstream = data.value?.upstream
  if (!upstream) return 'not_checked'
  if (upstream.status === 'available') {
    const success = Date.parse(upstream.last_success_at || '')
    if (!Number.isFinite(success) || now.value < success || now.value - success > 48 * 3600000) return 'stale'
  }
  return upstream.status
})
const cooldown = computed(() => Math.max(0, Math.ceil((Date.parse(data.value?.upstream.next_check_at || '') - now.value) / 1000)) || 0)
const states: Record<string, string> = { not_checked: '尚未检查', checking: '正在检查', available: '稳定版信息可用', stale: '缓存已过期或最近检查失败', unavailable: '版本信息暂不可用' }
const comparisons: Record<string, string> = { update_available: '有新稳定版可用', up_to_date: '与最新稳定版一致', ahead_of_stable: '版本高于当前稳定版', current_version_unknown: '当前版本无法比较', release_information_unavailable: '暂时无法判断是否需要更新' }
const errors: Record<string, string> = { upstream_unavailable: '无法连接官方版本源，请稍后重试。', invalid_response: '官方版本响应无法验证。', rate_limited: '官方版本源暂时限流，请在冷却结束后重试。', no_stable_release: '官方版本源暂无稳定版。', checker_not_attached: '版本检查服务尚未接入。' }
const date = (value?: string | null) => value ? new Date(value).toLocaleString() : '暂无记录'
function comparison(value: ReleaseComparison) {
  if (value.status !== 'current_version_unknown' && sourceStatus.value !== 'available') return comparisons.release_information_unavailable
  return comparisons[value.status] || '比较结果不可用'
}
async function load(check = false) {
  const current = ++generation
  controller?.abort()
  controller = new AbortController()
  busy.value = true
  error.value = ''
  // Do not leave another node's comparison visible while selection changes.
  data.value = null
  try {
    const result = await loadReleaseStatus(props.nodeId, check, controller.signal)
    if (current === generation) { data.value = result; now.value = Date.now() }
  } catch (reason) {
    if (current !== generation) return
    if (reason instanceof AdminAPIError && (reason.status === 401 || reason.status === 403)) { rejected.value = true; emit('unauthorized'); return }
    error.value = reason instanceof AdminAPIError && reason.status === 404 ? '节点已不存在，请重新选择。' : '版本信息读取失败，请重试。'
  } finally { if (current === generation) busy.value = false }
}
watch(() => props.nodeId, () => { void load() }, { immediate: true })
</script>

<template>
  <section v-if="!rejected" class="admin-panel release-awareness" aria-labelledby="release-title" :aria-busy="busy">
    <div class="release-heading"><h2 id="release-title">版本与升级</h2><div class="release-actions"><button class="soft-button" :disabled="busy" @click="load()">刷新版本状态</button><button class="soft-button" :disabled="busy || cooldown > 0" @click="load(true)">{{ busy ? '正在读取…' : '检查官方稳定版' }}</button></div></div>
    <p v-if="busy" role="status">正在读取版本信息…</p>
    <p v-if="error" role="alert">{{ error }} <button class="soft-button" @click="load()">重试读取</button></p>
    <template v-if="data">
      <p role="status">{{ states[sourceStatus] || '版本信息状态未知' }}<span v-if="cooldown > 0"> · 下次可检查：{{ date(data.upstream.next_check_at) }}</span></p>
      <p v-if="data.upstream.error_code" class="release-note">{{ errors[data.upstream.error_code] || '版本检查未成功，请稍后重试。' }}</p>
      <dl><dt>最新稳定版{{ sourceStatus === 'available' ? '' : '（缓存）' }}</dt><dd><a v-if="data.upstream.latest" :href="data.upstream.latest.url" target="_blank" rel="noopener noreferrer">{{ data.upstream.latest.tag }} · 发布说明</a><span v-else>暂无信息</span></dd><dt>最近检查</dt><dd>{{ date(data.upstream.checked_at) }}</dd><dt>最近成功</dt><dd>{{ date(data.upstream.last_success_at) }}</dd><dt>自动检查</dt><dd>{{ data.upstream.automatic ? '已开启，每 24 小时检查' : '已关闭，可手动检查' }}</dd><dt>Server 版本</dt><dd>{{ data.server.current_version || '未提供可比较的版本号' }} · {{ comparison(data.server) }}</dd></dl>
      <template v-if="data.agent">
        <h3>所选节点 Agent</h3>
        <dl><dt>最近声明版本</dt><dd>{{ data.agent.comparison.current_version || '未提供可比较的版本号' }} · {{ comparison(data.agent.comparison) }}</dd><dt>声明时间</dt><dd>{{ date(data.agent.hello_received_at) }}</dd></dl>
        <p class="release-note">Agent 版本来自最近一次握手，可能早于当前连接。请在该节点确认正在运行的版本和实例名。</p>
      </template>
      <p class="release-note">这里只比较软件版本；协议和功能支持以实际握手及能力诊断为准。升级前请阅读发布说明并验证备份。</p>
      <a :href="data.documentation_url" target="_blank" rel="noopener noreferrer">查看 Server / Agent 手动升级说明</a>
      <template v-if="serverOs === 'linux' && sourceStatus === 'available' && data.upstream.latest?.installer_tag && data.server.status === 'update_available'">
        <p class="release-note">使用 Linux 一键脚本部署的 Server，可在部署主机核对并执行以下命令（需已下载并审查 install.sh）：</p>
        <pre><code>sudo ./install.sh update server --version {{ data.upstream.latest.tag }}</code></pre>
      </template>
      <p class="release-note">版本检查访问固定的公开 GitHub Release API，不发送本机版本或节点资料。可用 MYPROBE_RELEASE_CHECKS_AUTO=false 关闭自动检查。</p>
    </template>
  </section>
</template>

<style scoped>
.release-awareness { padding: 20px; min-width: 0; }
.release-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; }
.release-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.release-awareness h2 { font-size: 17px; margin: 0; }.release-awareness h3 { font-size: 15px; }
.release-awareness dl { display: grid; grid-template-columns: minmax(80px,1fr) minmax(0,2fr); gap: 12px; font-size: 14px; }
.release-awareness dt,.release-note { color: var(--muted); }.release-awareness dd { margin: 0; text-align: right; }
.release-awareness p { line-height: 1.7; }.release-awareness dd,.release-awareness p,.release-awareness a { overflow-wrap: anywhere; }
.release-awareness pre { white-space: pre-wrap; overflow-wrap: anywhere; padding: 12px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
.release-awareness a { color: var(--accent); }
</style>
