<script setup lang="ts">
import type { SilenceRecord } from '../admin-api'
defineProps<{ records: SilenceRecord[] }>()
const date = (s: string) => new Date(s).toLocaleString('zh-CN', { hour12: false })
const decisions: Record<string,string> = { creation: '设置静默', observation: '静默期间评估', observation_gap: '观测间隔涉及静默', delivery: '队列通知抑制' }
const endings: Record<string,string> = { recovered: '观测已恢复正常', rule_disabled: '规则已停用', rule_changed: '规则配置已变更', rule_deleted: '规则或节点已删除' }
</script>
<template>
  <ol class="silence-records"><li v-for="item in records" :key="item.id">
    <p><strong>{{ item.active ? '核对时静默生效' : '静默已结束' }}</strong> · {{ item.creator_name }}</p>
    <p>{{ date(item.starts_at) }} → {{ date(item.ends_at) }}</p>
    <p v-if="item.reason" class="silence-reason">{{ item.reason }}</p><p v-else>未填写原因。</p>
    <p v-if="item.canceled_at">{{ item.canceler_name }} 于 {{ date(item.canceled_at) }} 取消。</p>
    <p v-if="item.closed_at">{{ date(item.closed_at) }} 结束：{{ endings[item.closure_reason || ''] || item.closure_reason }}</p>
    <p v-if="item.recorded_at">此通知抑制关联记录于 {{ date(item.recorded_at) }}。</p>
    <p v-for="d in item.decisions" :key="d.kind">{{ decisions[d.kind] || d.kind }}：{{ date(d.first_at) }} → {{ date(d.last_at) }}</p>
  </li></ol>
</template>
<style scoped>
.silence-records{padding-left:22px;display:grid;gap:12px;overflow-wrap:anywhere}.silence-records p{margin:4px 0;line-height:1.6}.silence-reason{white-space:pre-wrap;color:var(--text-primary)}
</style>
