<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { AdminAPIError, loadBackupStatus, loadChannels, runScheduledBackup, saveBackupSchedule, verifyBackupRecovery } from './admin-api'
import type { NotificationChannel } from './admin-api'
import type { BackupSchedule, BackupStatus } from './backup-api'

const emit = defineEmits<{ unauthorized: [] }>()
const form = reactive<BackupSchedule>({ enabled: false, directory: '', daily_time: '03:00', timezone: 'UTC', keep: 7, failure_channel_ids: [] })
const status = ref<BackupStatus | null>(null)
const channels = ref<NotificationChannel[]>([])
const busy = ref(false)
const loading = ref(false)
const stale = ref(false)
const error = ref('')
const notice = ref('')
const query = ref('')
const channelPage = ref(0)
const runPage = ref(0)
const notificationPage = ref(0)
const pageSize = 10
const availableChannels = computed(() => channels.value.filter(c => c.enabled && c.name.toLowerCase().includes(query.value.toLowerCase())))
const channelPages = computed(() => Math.max(1, Math.ceil(availableChannels.value.length / 25)))
const channelRows = computed(() => availableChannels.value.slice(channelPage.value * 25, channelPage.value * 25 + 25))
const unavailableSelections = computed(() => form.failure_channel_ids.filter(id => !channels.value.some(c => c.id === id && c.enabled)))
const active = computed(() => Boolean(status.value?.runtime.operation))
const runRows = computed(() => status.value?.runs.slice(runPage.value * pageSize, runPage.value * pageSize + pageSize) || [])
const notificationRows = computed(() => status.value?.notifications.slice(notificationPage.value * pageSize, notificationPage.value * pageSize + pageSize) || [])
const runPages = computed(() => Math.max(1, Math.ceil((status.value?.runs.length || 0) / pageSize)))
const notificationPages = computed(() => Math.max(1, Math.ceil((status.value?.notifications.length || 0) / pageSize)))
watch(query, () => { channelPage.value = 0 })
const labels: Record<string, string> = { running: '运行中', completed: '备份完成', failed: '失败', interrupted: '已中断', pending: '等待处理', inflight: '持有发送租约', delivered: '渠道已确认', unknown: '发送结果未知', canceled: '已取消' }
const codes: Record<string, string> = {
  backup_worker_busy: '已有备份或恢复演练正在执行，请稍后再试。', backup_passphrase_unavailable: '服务端独立备份口令文件不可读或不足 12 个字符。',
  backup_directory_and_failure_channel_required: '启用前请填写服务端目录，并选择至少一个失败提醒渠道。', invalid_backup_directory: '请输入服务端的绝对目录路径。',
  invalid_backup_schedule: '请检查每日时间、时区和保留份数（1—1000）。', invalid_backup_timezone: '时区无效，例如 Asia/Shanghai 或 UTC。',
  backup_failure_channel_unavailable: '失败提醒渠道已停用、被删除或无法解密，请重新选择。', backup_settings_save_failed: '设置保存失败，请检查失败提醒渠道后重试。',
  backup_insufficient_space: '备份或数据库所在磁盘的剩余空间不足。', backup_space_unavailable: '无法检查磁盘剩余空间。', backup_directory_unwritable: '备份目录不可写。',
  backup_directory_unavailable: '备份目录不可用。', backup_repository_in_use: '备份目录已被其他任务占用。', backup_directory_owner_mismatch: '该目录属于另一个数据库，请选择新目录。',
  backup_atomic_publish_unavailable: '该文件系统不支持原子发布，请选择本地目录。', backup_worker_unavailable: '备份工作线程不可用，请检查服务启动状态。',
  backup_process_interrupted: '备份进程中断或执行超时，上一份成功备份保留。', backup_cleanup_failed: '备份已生成，但旧备份清理失败。',
  recovery_application_key_unavailable: '恢复库中的通知凭据无法读取，请检查原始应用加密密钥。', recovery_artifact_unavailable: '备份已清理、文件改变或不属于当前目录。',
  recovery_decryption_failed: '备份解密失败，请检查原始口令和文件完整性。', recovery_database_invalid: '恢复后的数据库校验失败。',
  backup_schedule_disabled: '请先保存并启用定时备份。', channel_configuration_changed: '渠道配置改变，提醒已取消。', send_result_unknown: '发送可能已经发生，系统不会自动重发。',
}
const describe = (code?: string) => code ? codes[code] || `操作未完成，错误分类：${code}` : ''
const date = (value?: string) => value ? new Date(value).toLocaleString() : '尚无记录'
let initialized = false
let alive = true
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
function failure(reason: unknown) {
  if (reason instanceof AdminAPIError && (reason.status === 401 || reason.status === 403)) {
    status.value = null; channels.value = []; form.directory = ''; form.failure_channel_ids = []; emit('unauthorized'); return
  }
  error.value = reason instanceof AdminAPIError ? describe(reason.message) : '读取备份状态失败，请重试。'
}
async function refresh() {
  if (loading.value || !alive) return
  loading.value = true; controller = new AbortController()
  const requestController = controller
  const timeout = setTimeout(() => requestController.abort(), 12000)
  try {
    const [statusResult, channelResult] = await Promise.allSettled([loadBackupStatus(controller.signal), loadChannels(controller.signal)])
    if (!alive) return
    for (const result of [statusResult, channelResult]) {
      if (result.status === 'rejected' && result.reason instanceof AdminAPIError && (result.reason.status === 401 || result.reason.status === 403)) { failure(result.reason); return }
    }
    if (statusResult.status === 'rejected') throw statusResult.reason
    if (channelResult.status === 'rejected') throw channelResult.reason
    const next = statusResult.value, destinations = channelResult.value
    status.value = next; channels.value = destinations.channels; stale.value = false; error.value = ''
    if (!initialized) { Object.assign(form, next.config); initialized = true }
    channelPage.value = Math.min(channelPage.value, channelPages.value - 1)
    runPage.value = Math.min(runPage.value, runPages.value - 1)
    notificationPage.value = Math.min(notificationPage.value, notificationPages.value - 1)
  } catch (reason) { if (alive) { stale.value = true; failure(reason) } }
  finally { clearTimeout(timeout); loading.value = false }
}
async function action(kind: 'save' | 'run' | 'verify', id = '') {
  busy.value = true; error.value = ''; notice.value = ''
  try {
    if (kind === 'save') { await saveBackupSchedule({ ...form, failure_channel_ids: [...form.failure_channel_ids] }); notice.value = '设置已保存。' }
    else if (kind === 'run') { await runScheduledBackup(); notice.value = '备份任务已接受。可刷新或稍后返回查看结果。' }
    else { await verifyBackupRecovery(id); notice.value = '隔离恢复演练已接受，结果完成后显示。' }
    if (alive) await refresh()
  } catch (reason) { if (alive) failure(reason) }
  finally { busy.value = false }
}
async function poll() { await refresh(); if (alive) timer = setTimeout(poll, active.value ? 2000 : 15000) }
onMounted(() => { void poll() })
onUnmounted(() => { alive = false; clearTimeout(timer); controller?.abort() })
</script>

<template>
  <section class="scheduled-backups admin-panel" aria-labelledby="scheduled-backups-title">
    <div class="backup-heading"><div><span class="eyebrow">DAILY BACKUP</span><h2 id="scheduled-backups-title">定时备份与恢复演练</h2></div><button class="soft-button" :disabled="loading" @click="refresh">刷新备份状态</button></div>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="stale" class="backup-note">当前显示的是上次读取结果，状态可能已经变化。刷新成功后再操作。</p>
    <p v-if="!status && loading" role="status">正在读取备份设置…</p>
    <template v-if="status">
      <p class="backup-note">备份口令独立托管于服务端文件（MYPROBE_BACKUP_PASSPHRASE_FILE），不通过本页面提交。恢复须保留 .mpb 文件、原始备份口令和 MYPROBE_ENCRYPTION_KEY。</p>
      <p>工作线程：{{ status.runtime.running ? '运行中' : '尚未启动或已退出' }} · 口令文件：{{ status.passphrase_ready ? '可读取' : '不可用' }}<span v-if="active"> · {{ status.runtime.operation === 'recovery' ? '正在执行隔离恢复演练' : '正在生成备份' }}</span></p>
      <p v-if="status.runtime.error_code" role="status">最近任务错误：{{ describe(status.runtime.error_code) }}</p>
      <form @submit.prevent="action('save')">
        <label class="check-line"><input v-model="form.enabled" type="checkbox">启用每日备份</label>
        <div class="backup-fields">
          <label>服务端备份目录<input v-model="form.directory" autocomplete="off" placeholder="服务端绝对路径"></label>
          <label>每日时间<input v-model="form.daily_time" type="time" required></label>
          <label>时区<input v-model="form.timezone" required list="backup-timezones"><datalist id="backup-timezones"><option value="UTC" /><option value="Asia/Shanghai" /></datalist></label>
          <label>保留成功备份份数<input v-model.number="form.keep" type="number" min="1" max="1000" required></label>
        </div>
        <fieldset><legend>失败提醒渠道（已选 {{ form.failure_channel_ids.length }} / 20）</legend><label>搜索渠道<input v-model="query" type="search" placeholder="按名称搜索"></label>
          <div v-for="id in unavailableSelections" :key="id" class="backup-pages"><span>已选渠道已停用或删除</span><button type="button" @click="form.failure_channel_ids = form.failure_channel_ids.filter(value => value !== id)">移除不可用渠道</button></div>
          <div class="channel-list"><label v-for="channel in channelRows" :key="channel.id" class="check-line"><input v-model="form.failure_channel_ids" type="checkbox" :value="channel.id" :disabled="form.failure_channel_ids.length >= 20 && !form.failure_channel_ids.includes(channel.id)">{{ channel.name }} · {{ channel.kind }}</label></div>
          <p v-if="!channelRows.length">暂无可用渠道，请先在通知中心配置并启用渠道。</p>
          <div v-if="channelPages > 1" class="backup-pages"><button type="button" :disabled="channelPage === 0" @click="channelPage--">上一页渠道</button><span>{{ channelPage + 1 }} / {{ channelPages }}</span><button type="button" :disabled="channelPage + 1 >= channelPages" @click="channelPage++">下一页渠道</button></div>
        </fieldset>
        <p class="backup-note">按当前日期补跑一次已错过的每日时间，不补跑往日。重启、重复时刻及时间回拨不会重复当天任务。保留清理只删除本系统签名且内容未变的成功文件。</p>
        <div class="backup-actions"><button class="primary-button" :disabled="busy || loading || active || stale" type="submit">保存备份设置</button><button class="soft-button" type="button" :disabled="busy || loading || active || stale || !status.config.enabled" @click="action('run')">立即执行一次</button></div>
      </form>
      <h3>最近备份任务</h3><p class="backup-note">最多显示最近 50 条记录。文件生成校验不代表恢复演练；已清理的文件仍保留历史完成记录。</p>
      <p v-if="!status.runs.length">尚无备份任务。</p>
      <article v-for="run in runRows" :key="run.id" class="backup-row"><strong>{{ labels[run.status] || run.status }} · {{ run.trigger === 'daily' ? '每日任务' : '手动执行' }}</strong><p>开始：{{ date(run.started_at) }} · 完成：{{ date(run.finished_at) }}</p><p v-if="run.error_code">{{ describe(run.error_code) }}</p>
        <template v-if="run.artifact"><p class="backup-file">{{ run.artifact.file_name }} · {{ (run.artifact.bytes / 1048576).toFixed(2) }} MiB</p><details><summary>SHA-256 校验和</summary><code>{{ run.artifact.sha256 }}</code></details>
          <p>{{ run.artifact.recovery_verified ? '已验证可恢复（隔离数据库读取与通知凭据解密）' : '备份完成，尚未验证可恢复' }}<span v-if="run.artifact.recovery_verified_at"> · {{ date(run.artifact.recovery_verified_at) }}</span></p>
          <p v-if="run.artifact.recovery_evidence">隔离读取：{{ run.artifact.recovery_evidence.nodes }} 个节点，{{ run.artifact.recovery_evidence.users }} 个用户，{{ run.artifact.recovery_evidence.reports }} 条原始样本，{{ run.artifact.recovery_evidence.channels }} 个通知渠道。</p>
          <button class="soft-button" :disabled="busy || loading || active || stale || run.status === 'running'" @click="action('verify', run.id)">执行隔离恢复演练</button>
        </template>
      </article>
      <div v-if="runPages > 1" class="backup-pages"><button :disabled="runPage === 0" @click="runPage--">上一页任务</button><span>{{ runPage + 1 }} / {{ runPages }}</span><button :disabled="runPage + 1 >= runPages" @click="runPage++">下一页任务</button></div>
      <h3>备份失败提醒</h3><p v-if="!status.notifications.length">尚无失败提醒记录。</p>
      <article v-for="item in notificationRows" :key="item.id" class="backup-row"><strong>{{ item.channel_name || '渠道已删除' }} · {{ labels[item.status] || item.status }}</strong><p>{{ date(item.occurred_at) }} · {{ item.provider }} · 已尝试 {{ item.attempt_count }} 次</p><p>{{ describe(item.error_class || item.error_code) }}</p><p v-if="item.status === 'pending'">计划处理时间：{{ date(item.available_at) }}，仍须通过渠道等待和配置检查。</p></article>
      <div v-if="notificationPages > 1" class="backup-pages"><button :disabled="notificationPage === 0" @click="notificationPage--">上一页提醒</button><span>{{ notificationPage + 1 }} / {{ notificationPages }}</span><button :disabled="notificationPage + 1 >= notificationPages" @click="notificationPage++">下一页提醒</button></div>
      <p class="backup-note">演练不会替换当前数据库或发送恢复库中的通知。验证范围是本地数据库和凭据可读；切换服务、Agent 重连及外部渠道恢复仍需独立部署演练。</p>
    </template>
  </section>
</template>

<style scoped>
.scheduled-backups { margin-top: 18px; padding: 20px; min-width: 0; }
.backup-heading,.backup-actions,.backup-pages { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.backup-heading { justify-content: space-between; }.backup-heading h2 { margin: 6px 0; }
.backup-fields { display: grid; grid-template-columns: repeat(auto-fit,minmax(min(100%,220px),1fr)); gap: 14px; margin: 16px 0; }
label { display: grid; gap: 8px; font-size: 14px; min-width: 0; }.check-line { display: flex; align-items: center; overflow-wrap: anywhere; }
input:not([type=checkbox]) { width: 100%; min-width: 0; box-sizing: border-box; padding: 10px; border: 1px solid var(--border); border-radius: 8px; color: var(--text); background: var(--surface); }
fieldset { margin: 18px 0; padding: 14px; border: 1px solid var(--border); border-radius: 10px; min-width: 0; }
.channel-list { display: grid; gap: 10px; margin-top: 14px; }.backup-note { color: var(--muted); line-height: 1.7; }
.backup-row { padding: 16px 0; border-top: 1px solid var(--border); }.backup-row p { margin: 8px 0; }
.scheduled-backups p,.backup-file,code { overflow-wrap: anywhere; }code { display: block; margin: 10px 0; }summary { cursor: pointer; }
.backup-pages { margin: 14px 0; }.backup-pages button { padding: 8px 12px; background: var(--surface); color: var(--text); border: 1px solid var(--border); border-radius: 8px; }
h3 { margin-top: 24px; }
</style>
