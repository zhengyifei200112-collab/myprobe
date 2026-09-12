<script setup lang="ts">
withDefaults(defineProps<{ label: string; value: string | number; detail?: string; tone?: 'default' | 'accent' | 'success' | 'warning' | 'danger' }>(), { detail: '', tone: 'default' })
</script>

<template><div class="ds-stat" :class="`ds-stat--${tone}`"><span>{{ label }}</span><strong class="ds-tabular">{{ value }}</strong><small v-if="detail">{{ detail }}</small></div></template>

<style scoped>
.ds-stat { display: grid; gap: var(--space-1); }.ds-stat > span { color: var(--text-secondary); font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); }.ds-stat strong { color: var(--text-primary); font-size: var(--font-size-xl); line-height: var(--line-height-tight); }.ds-stat small { color: var(--text-tertiary); font-size: var(--font-size-xs); }.ds-stat--accent strong { color: var(--accent); }.ds-stat--success strong { color: var(--success); }.ds-stat--warning strong { color: var(--warning); }.ds-stat--danger strong { color: var(--danger); }
</style>
