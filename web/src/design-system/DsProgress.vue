<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ value?: number; max?: number; label: string; tone?: 'accent' | 'success' | 'warning' | 'danger'; showValue?: boolean }>(), { value: 0, max: 100, tone: 'accent', showValue: false })
const percent = computed(() => props.max > 0 ? Math.min(100, Math.max(0, props.value / props.max * 100)) : 0)
</script>

<template>
  <div class="ds-progress">
    <div v-if="showValue" class="ds-progress__meta"><span>{{ label }}</span><strong class="ds-tabular">{{ Math.round(percent) }}%</strong></div>
    <div class="ds-progress__track" role="progressbar" :aria-label="label" :aria-valuemin="0" :aria-valuemax="max" :aria-valuenow="value"><i :class="`ds-progress__fill--${tone}`" :style="{ width: `${percent}%` }"></i></div>
  </div>
</template>

<style scoped>
.ds-progress { display: grid; gap: var(--space-2); }.ds-progress__meta { display: flex; justify-content: space-between; gap: var(--space-3); color: var(--text-secondary); font-size: var(--font-size-xs); }.ds-progress__meta strong { color: var(--text-primary); }
.ds-progress__track { height: .5rem; overflow: hidden; border-radius: var(--radius-round); background: var(--surface-tertiary); }.ds-progress__track i { display: block; min-width: 1px; height: 100%; border-radius: inherit; background: var(--accent); transition: width var(--duration-slow) var(--ease-standard); }.ds-progress__track .ds-progress__fill--success { background: var(--success); }.ds-progress__track .ds-progress__fill--warning { background: var(--warning); }.ds-progress__track .ds-progress__fill--danger { background: var(--danger); }
</style>
