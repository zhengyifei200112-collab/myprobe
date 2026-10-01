<script setup lang="ts">
import type { PublicNode } from '../types'
import { attentionReasons, latencyValue, nodeState, stateLabels } from './discovery'
import { aggregateNode, commonByteUnit, formatBytesInUnit, formatPercent, lastReport } from './metrics'

defineProps<{ nodes: PublicNode[]; target: string; now: Date }>()
defineEmits<{ open: [item: PublicNode] }>()
function rate(item: PublicNode, direction: 'up' | 'down') {
  if (nodeState(item) !== 'online') return '—'
  const metrics = aggregateNode(item)
  const value = direction === 'up' ? metrics.txRate : metrics.rxRate
  return formatBytesInUnit(value, commonByteUnit([metrics.txRate, metrics.rxRate]), '/s')
}
</script>

<template>
  <div class="node-table-scroll" role="region" aria-label="节点对比表，可横向滚动" tabindex="0">
    <table class="node-table">
      <caption>当前筛选节点；点击名称查看历史。缺失或陈旧的实时数值显示为 —。</caption>
      <thead><tr><th scope="col">名称</th><th scope="col">状态 / 需要关注</th><th scope="col">CPU</th><th scope="col">内存</th><th scope="col">上传</th><th scope="col">下载</th><th scope="col">目标延迟</th><th scope="col">到期时间</th></tr></thead>
      <tbody><tr v-for="item in nodes" :key="item.node.id" :data-node-id="item.node.id">
        <th scope="row"><button type="button" @click="$emit('open', item)">{{ item.node.name }}</button><small>{{ (item.node.tags || []).join(' · ') }}</small></th>
        <td><span :class="['table-state', nodeState(item)]">{{ stateLabels[nodeState(item)] }}</span><small>{{ attentionReasons(item, now.getTime()).join(' · ') }}</small><small>{{ lastReport(item) }}</small></td>
        <td>{{ nodeState(item) === 'online' ? formatPercent(item.report?.cpu.usage_percent) : '—' }}</td>
        <td>{{ nodeState(item) === 'online' ? formatPercent(item.report?.memory.usage_percent) : '—' }}</td>
        <td>{{ rate(item, 'up') }}</td><td>{{ rate(item, 'down') }}</td>
        <td>{{ !target ? '先选择目标' : latencyValue(item, target) == null ? '—' : `${latencyValue(item, target)!.toFixed(1)} ms` }}</td>
        <td>{{ item.node.expires_at ? new Date(item.node.expires_at).toLocaleDateString('zh-CN') : '—' }}</td>
      </tr></tbody>
    </table>
  </div>
</template>
