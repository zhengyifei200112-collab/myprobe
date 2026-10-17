<script setup lang="ts">
import type { NodeTimeline } from '../types'
import { DsButton } from '../design-system'
import { eventLabel } from './timeline-view'

defineProps<{ timeline: NodeTimeline; admin: boolean; loading: boolean; error: string; focused?: string }>()
defineEmits<{ more: []; focus: [at: string] }>()
</script>

<template>
  <section class="detail-card detail-timeline" aria-label="节点事件时间线">
    <h2>事件时间线</h2>
    <p class="detail-muted">{{ admin ? '包含本节点的告警、维护和配置保存记录。告警首次异常与达到门槛分别展示，配置保存不代表 Agent 已应用。' : '公开页仅展示上报观测；告警、维护和管理操作仅管理员可见。' }}</p>
    <p class="detail-muted">上报间隔异常按当前采集与上报周期估算（{{ timeline.gap_threshold_seconds }} 秒），不等同于主机离线。运行时间下降仅表示重启迹象，Agent 重连不会直接判为重启。</p>
    <p class="detail-muted">上报事件受原始样本保留范围限制，汇总曲线不能还原已清理事件。{{ timeline.observations_truncated ? '仅检查此窗口最新 100000 条原始样本，请缩小范围以查看更早观测。' : '' }}{{ timeline.observation_start ? `此窗口可检查的上报从 ${new Date(timeline.observation_start).toLocaleString('zh-CN', { hour12: false })} 开始。` : '此窗口没有可检查的原始上报样本。' }}</p>
    <ol v-if="timeline.events.length" class="detail-event-list">
      <li v-for="event in timeline.events" :key="event.id" :data-event-kind="event.kind">
        <div><time :datetime="event.time" :title="event.time">{{ new Date(event.time).toLocaleString('zh-CN', { hour12: false }) }}</time><strong>{{ eventLabel(event) }}</strong><span v-if="event.approximate" class="detail-muted">观测 / 估算</span></div>
        <div class="detail-event-actions"><DsButton :aria-pressed="focused === event.time" :aria-label="`定位曲线：${eventLabel(event)}`" @click="$emit('focus', event.time)">定位曲线</DsButton><a v-if="admin && event.incident_id" :href="`/admin?incident=${encodeURIComponent(event.incident_id)}`" class="soft-button">查看告警</a></div>
      </li>
    </ol>
    <p v-else class="detail-empty">此范围内没有可展示的事件，不能据此判断主机一直正常。</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <DsButton v-if="timeline.next_cursor" :loading="loading" @click="$emit('more')">加载更早事件</DsButton>
    <p class="detail-muted" role="status">{{ focused ? `曲线已定位到 ${new Date(focused).toLocaleString('zh-CN', { hour12: false })}` : '选择事件可在各曲线中高亮并缩放到对应时间。' }}</p>
  </section>
</template>
