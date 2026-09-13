<script setup lang="ts">
import { computed } from 'vue'
import { DsBadge, DsCard, DsProgress, DsStatusIndicator } from '../design-system'
import type { PublicNode } from '../types'
import {
  aggregateNode,
  expiry,
  expiryDate,
  formatBytes,
  formatBytesInUnit,
  formatUptime,
  lastReport,
  latencyText,
  maskedIP,
  commonByteUnit,
  offlineDuration,
  osName,
  percent,
  price,
  resourceTone,
} from './metrics'

const props = defineProps<{ item: PublicNode; displayMode: 'compact' | 'detailed'; now: Date }>()
defineEmits<{ open: [] }>()

const metrics = computed(() => aggregateNode(props.item))
const rateUnit = computed(() => commonByteUnit([metrics.value.txRate, metrics.value.rxRate]))
const totalUnit = computed(() => commonByteUnit([metrics.value.txTotal, metrics.value.rxTotal]))
const cycleUnit = computed(() => commonByteUnit([props.item.traffic?.tx_bytes || 0, props.item.traffic?.rx_bytes || 0]))
const capacityUnit = computed(() => commonByteUnit([props.item.report?.memory.total_bytes || 0, metrics.value.diskTotal]))
const status = computed(() => !props.item.online ? 'offline' : props.item.stale ? 'warning' : 'online')
const statusLabel = computed(() => !props.item.online ? '离线' : props.item.stale ? '数据延迟' : '在线')
const healthTitle = computed(() => !props.item.online ? '节点离线' : props.item.stale ? '连接在线，数据可能延迟' : '运行正常')
const healthDescription = computed(() => {
  if (!props.item.online) return `已离线 ${offlineDuration(props.item, props.now)}`
  if (!props.item.report) return '等待 Agent 首次上报'
  return `已稳定运行 ${formatUptime(props.item.report.uptime_seconds)}`
})

function countryCode(code: string) {
  const normalized = code?.trim().toLowerCase()
  return /^[a-z]{2}$/.test(normalized) ? normalized : ''
}
</script>

<template>
  <DsCard
    as="article"
    padding="none"
    interactive
    class="public-node-card"
    :class="[`public-node-card--${status}`, { 'public-node-card--detailed': displayMode === 'detailed' }]"
  >
    <div class="public-node-card__inner">
      <header class="public-node-card__header">
        <div class="public-node-card__identity">
          <span
            class="public-node-card__flag"
            role="img"
            :aria-label="countryCode(item.node.country_code) ? `${item.node.country_code.toUpperCase()} 国旗` : '未设置国家或地区'"
          >
            <span
              v-if="countryCode(item.node.country_code)"
              class="country-flag"
              :class="`flag:${countryCode(item.node.country_code).toUpperCase()}`"
              aria-hidden="true"
            ></span>
            <svg v-else viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5"/><path d="M3.8 12h16.4M12 3.5c2.4 2.3 3.6 5.1 3.6 8.5S14.4 18.2 12 20.5M12 3.5C9.6 5.8 8.4 8.6 8.4 12s1.2 6.2 3.6 8.5"/></svg>
          </span>
          <div>
            <h3>{{ item.node.name }}</h3>
            <span>{{ osName(item) }}</span>
          </div>
        </div>
        <DsStatusIndicator :status="status" :label="statusLabel" />
      </header>

      <section class="public-node-card__health" :aria-label="healthTitle">
        <strong>{{ healthTitle }}</strong>
        <p>{{ healthDescription }}</p>
      </section>

      <div v-if="!item.online" class="public-node-card__offline" role="status">
        <span>最后上报</span>
        <strong>{{ lastReport(item) }}</strong>
      </div>

      <dl class="public-node-card__facts">
        <div><dt>公网 IP</dt><dd class="ds-tabular">{{ maskedIP(item.report?.public_ip) }}</dd></div>
        <div><dt>在线时长</dt><dd>{{ formatUptime(item.report?.uptime_seconds) }}</dd></div>
        <div><dt>本月流量</dt><dd class="ds-tabular">{{ formatBytes((item.traffic?.tx_bytes || 0) + (item.traffic?.rx_bytes || 0)) }}</dd></div>
      </dl>

      <section class="public-node-card__resources" aria-label="资源使用">
        <div>
          <span><b>CPU</b><strong class="ds-tabular">{{ percent(item.report?.cpu.usage_percent) }}</strong></span>
          <DsProgress :value="item.report?.cpu.usage_percent" label="CPU 使用率" :tone="resourceTone(item.report?.cpu.usage_percent)" />
        </div>
        <div>
          <span><b>内存</b><strong class="ds-tabular">{{ percent(item.report?.memory.usage_percent) }}</strong></span>
          <DsProgress :value="item.report?.memory.usage_percent" label="内存使用率" :tone="resourceTone(item.report?.memory.usage_percent)" />
        </div>
        <div>
          <span><b>磁盘</b><strong class="ds-tabular">{{ percent(metrics.diskPercent) }}</strong></span>
          <DsProgress :value="metrics.diskPercent" label="磁盘使用率" :tone="resourceTone(metrics.diskPercent)" />
        </div>
      </section>

      <section class="public-node-card__network" aria-label="网络实时速率">
        <div><span>网络实时速率</span><small>{{ item.node.traffic_reset_day ? `每月 ${item.node.traffic_reset_day} 日重置` : '自然月重置' }}</small></div>
        <div class="public-node-card__rates">
          <span><i aria-hidden="true">↑</i><small>上传</small><strong class="ds-tabular">{{ formatBytesInUnit(metrics.txRate, rateUnit, '/s') }}</strong></span>
          <span><i aria-hidden="true">↓</i><small>下载</small><strong class="ds-tabular">{{ formatBytesInUnit(metrics.rxRate, rateUnit, '/s') }}</strong></span>
        </div>
      </section>

      <div v-if="displayMode === 'detailed'" class="public-node-card__details">
        <dl class="public-node-card__hardware" aria-label="硬件概况">
          <div><dt>处理器</dt><dd>{{ item.report?.cpu.logical_cores || '—' }} 核</dd></div>
          <div><dt>内存容量</dt><dd>{{ formatBytesInUnit(item.report?.memory.total_bytes || 0, capacityUnit) }}</dd></div>
          <div><dt>磁盘容量</dt><dd>{{ formatBytesInUnit(metrics.diskTotal, capacityUnit) }}</dd></div>
        </dl>
        <section class="public-node-card__traffic" aria-label="流量详情">
          <div><span>{{ item.node.use_since_boot ? '开机累计' : '累计流量' }}</span><strong>↑ {{ formatBytesInUnit(metrics.txTotal, totalUnit) }} · ↓ {{ formatBytesInUnit(metrics.rxTotal, totalUnit) }}</strong></div>
          <div><span>本周期</span><strong>↑ {{ formatBytesInUnit(item.traffic?.tx_bytes || 0, cycleUnit) }} · ↓ {{ formatBytesInUnit(item.traffic?.rx_bytes || 0, cycleUnit) }}</strong></div>
        </section>

        <section class="public-node-card__latency" aria-label="网络延迟">
          <header><strong>网络延迟</strong><small>最近探测</small></header>
          <div class="public-node-card__latency-grid">
            <div v-for="latency in item.latency?.slice(0, 3)" :key="latency.target_id">
              <span><i :class="{ failed: latency.success === false }" aria-hidden="true"></i>{{ latency.name }}</span>
              <strong :class="{ failed: latency.success === false }">{{ latencyText(latency.success, latency.latency_ms, latency.error_class) }}</strong>
            </div>
            <div v-if="!item.latency?.length">
              <span>{{ item.node.latency_mode === 'tcping' ? 'TCPing' : 'Ping' }}</span>
              <strong>等待后台分配目标</strong>
            </div>
          </div>
        </section>

        <section class="public-node-card__commercial" aria-label="服务信息">
          <DsBadge tone="accent">{{ price(item) }}<template v-if="item.node.billing_cycle">/{{ item.node.billing_cycle }}</template></DsBadge>
          <span :class="{ overdue: item.commercial?.expired }">{{ expiry(item) }}</span>
          <span v-if="expiryDate(item)">{{ expiryDate(item) }}</span>
        </section>

        <div v-if="item.node.custom_badges?.length || item.node.custom_links?.length || item.node.custom_html" class="custom-display">
          <div v-if="item.node.custom_badges?.length" class="custom-badges"><span v-for="badge in item.node.custom_badges" :key="`${badge.label}-${badge.color}`" :class="`custom-badge ${badge.color}`">{{ badge.label }}</span></div>
          <div v-if="item.node.custom_links?.length" class="custom-links"><a v-for="link in item.node.custom_links" :key="link.url" :href="link.url" target="_blank" rel="noopener noreferrer">{{ link.label }}</a></div>
          <div v-if="item.node.custom_html" class="custom-html" v-html="item.node.custom_html"></div>
        </div>
      </div>

      <footer class="public-node-card__footer">
        <span>{{ item.report?.captured_at ? `最后更新 ${lastReport(item)}` : '暂无上报数据' }}</span>
        <button type="button" :aria-label="`查看 ${item.node.name} 的历史数据`" @click="$emit('open')">
          查看历史
          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 3 5 5-5 5"/></svg>
        </button>
      </footer>
    </div>
  </DsCard>
</template>

<style scoped>
.public-node-card { position: relative; min-width: 0; overflow: hidden; }
.public-node-card::before { position: absolute; inset: 0 auto 0 0; width: 3px; background: var(--success); content: ''; }
.public-node-card--warning::before { background: var(--warning); }
.public-node-card--offline::before { background: var(--danger); }
.public-node-card--offline { background: color-mix(in srgb, var(--danger) 2.5%, var(--surface)); }
.public-node-card__inner { padding: var(--space-6); display: grid; gap: var(--space-5); }
.public-node-card__header { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-4); }
.public-node-card__identity { min-width: 0; display: flex; align-items: center; gap: var(--space-3); }
.public-node-card__identity > div { min-width: 0; display: grid; gap: var(--space-1); }
.public-node-card__identity h3 { margin: 0; overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-lg); line-height: var(--line-height-tight); }
.public-node-card__identity span { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__flag { width: 2.5rem; height: 1.875rem; flex: 0 0 auto; padding: 2px; display: grid; place-items: center; overflow: hidden; border: 1px solid var(--border); border-radius: var(--radius-badge); background: var(--surface-solid); color: var(--text-tertiary); }
.public-node-card__flag .country-flag { width: 100%; height: 100%; border-radius: .4rem; background-size: cover; }
.public-node-card__flag svg { width: 1.15rem; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.6; }
.public-node-card__health { display: grid; gap: var(--space-1); }
.public-node-card__health strong { color: var(--text-primary); font-size: var(--font-size-md); }
.public-node-card__health p { margin: 0; color: var(--text-secondary); font-size: var(--font-size-sm); }
.public-node-card__offline { padding: var(--space-3) var(--space-4); display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); border: 1px solid color-mix(in srgb, var(--danger) 22%, var(--border)); border-radius: var(--radius-control); background: var(--danger-soft); color: var(--danger); font-size: var(--font-size-xs); }
.public-node-card__offline strong { color: var(--text-primary); font-variant-numeric: tabular-nums; }
.public-node-card__facts { margin: 0; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); }
.public-node-card__facts > div { min-width: 0; padding: var(--space-3); border-radius: var(--radius-control); background: var(--surface-secondary); }
.public-node-card__facts dt { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__facts dd { margin: var(--space-1) 0 0; overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-sm); font-weight: var(--font-weight-semibold); }
.public-node-card__resources { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-4); }
.public-node-card__resources > div { min-width: 0; }
.public-node-card__resources > div > span { margin-bottom: var(--space-2); display: flex; justify-content: space-between; gap: var(--space-2); color: var(--text-secondary); font-size: var(--font-size-xs); }
.public-node-card__resources b { font-weight: var(--font-weight-medium); }
.public-node-card__resources strong { color: var(--text-primary); }
.public-node-card__network { padding-top: var(--space-4); display: grid; grid-template-columns: minmax(8rem, .75fr) minmax(0, 1.25fr); align-items: center; gap: var(--space-4); border-top: 1px solid var(--border); }
.public-node-card__network > div:first-child { display: grid; gap: var(--space-1); }
.public-node-card__network > div:first-child > span { color: var(--text-primary); font-size: var(--font-size-sm); font-weight: var(--font-weight-semibold); }
.public-node-card__network small { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__rates { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-3); }
.public-node-card__rates > span { min-width: 0; display: grid; grid-template-columns: auto auto; align-items: baseline; justify-content: start; gap: var(--space-1); }
.public-node-card__rates i { color: var(--accent); font-style: normal; }
.public-node-card__rates > span:last-child i { color: var(--info); }
.public-node-card__rates strong { grid-column: 1 / -1; overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-sm); }
.public-node-card__details { display: grid; gap: var(--space-4); }
.public-node-card__hardware { margin: 0; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); }
.public-node-card__hardware div { min-width: 0; }
.public-node-card__hardware dt { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__hardware dd { margin: var(--space-1) 0 0; overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-xs); font-weight: var(--font-weight-semibold); }
.public-node-card__traffic { padding: var(--space-4); display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-4); border-radius: var(--radius-control); background: var(--surface-secondary); }
.public-node-card__traffic div { min-width: 0; display: grid; gap: var(--space-1); }
.public-node-card__traffic span { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__traffic strong { overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-xs); }
.public-node-card__latency { padding-top: var(--space-4); border-top: 1px solid var(--border); }
.public-node-card__latency header { display: flex; justify-content: space-between; gap: var(--space-3); }
.public-node-card__latency header strong { font-size: var(--font-size-sm); }
.public-node-card__latency header small { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__latency-grid { margin-top: var(--space-3); display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); }
.public-node-card__latency-grid > div { min-width: 0; display: grid; gap: var(--space-1); }
.public-node-card__latency-grid span { overflow: hidden; color: var(--text-tertiary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-xs); }
.public-node-card__latency-grid span i { width: .45rem; height: .45rem; margin-right: var(--space-1); display: inline-block; border-radius: 50%; background: var(--success); }
.public-node-card__latency-grid span i.failed { background: var(--danger); }
.public-node-card__latency-grid strong { overflow: hidden; color: var(--text-primary); text-overflow: ellipsis; white-space: nowrap; font-size: var(--font-size-xs); }
.public-node-card__latency-grid strong.failed { color: var(--danger); }
.public-node-card__commercial { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-3); color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__commercial .overdue { color: var(--danger); }
.public-node-card__footer { padding-top: var(--space-4); display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); border-top: 1px solid var(--border); color: var(--text-tertiary); font-size: var(--font-size-xs); }
.public-node-card__footer > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.public-node-card__footer button { min-height: 2.25rem; padding: 0 var(--space-3); display: inline-flex; align-items: center; gap: var(--space-1); border: 0; border-radius: var(--radius-control); background: transparent; color: var(--accent); cursor: pointer; font-size: var(--font-size-xs); font-weight: var(--font-weight-semibold); }
.public-node-card__footer button:hover { background: var(--accent-soft); }
.public-node-card__footer svg { width: .85rem; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.8; }
.custom-display { padding: var(--space-3); display: grid; gap: var(--space-2); border-radius: var(--radius-control); background: var(--surface-secondary); }
.custom-badges, .custom-links { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.custom-badge, .custom-links a { padding: .35rem .55rem; border-radius: var(--radius-badge); font-size: var(--font-size-xs); font-weight: var(--font-weight-semibold); }
.custom-badge.gray { background: color-mix(in srgb, var(--text-secondary) 12%, transparent); color: var(--text-secondary); }
.custom-badge.blue { background: var(--accent-soft); color: var(--accent); }
.custom-badge.green { background: var(--success-soft); color: var(--success); }
.custom-badge.orange { background: var(--warning-soft); color: var(--warning); }
.custom-badge.red { background: var(--danger-soft); color: var(--danger); }
.custom-links a { border: 1px solid var(--border); color: var(--accent); text-decoration: none; }
.custom-html { overflow-wrap: anywhere; color: var(--text-secondary); font-size: var(--font-size-xs); line-height: var(--line-height-body); }
@media (max-width: 680px) {
  .public-node-card__inner { padding: var(--space-5); }
  .public-node-card__facts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .public-node-card__facts > div:last-child { grid-column: 1 / -1; }
  .public-node-card__network { grid-template-columns: 1fr; }
  .public-node-card__latency-grid { grid-template-columns: 1fr; }
  .public-node-card__footer { align-items: flex-start; }
}
</style>
