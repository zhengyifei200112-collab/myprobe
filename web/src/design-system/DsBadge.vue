<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'neutral' | 'accent' | 'success' | 'warning' | 'danger'; dot?: boolean }>(), { tone: 'neutral', dot: false })
</script>

<template><span class="ds-badge" :class="`ds-badge--${tone}`"><i v-if="dot" aria-hidden="true"></i><slot></slot></span></template>

<style scoped>
.ds-badge { min-height: 1.625rem; padding: .25rem .625rem; display: inline-flex; align-items: center; gap: .4rem; border: 1px solid var(--border); border-radius: var(--radius-badge); background: var(--surface-secondary); color: var(--text-secondary); font-size: var(--font-size-xs); font-weight: var(--font-weight-semibold); line-height: 1; }
.ds-badge i { width: .4rem; height: .4rem; border-radius: 50%; background: currentColor; }
.ds-badge--accent { border-color: color-mix(in srgb, var(--accent) 24%, var(--border)); background: var(--accent-soft); color: var(--accent); }
.ds-badge--success { border-color: color-mix(in srgb, var(--success) 24%, var(--border)); background: var(--success-soft); color: var(--success); }
.ds-badge--warning { border-color: color-mix(in srgb, var(--warning) 24%, var(--border)); background: var(--warning-soft); color: var(--warning); }
.ds-badge--danger { border-color: color-mix(in srgb, var(--danger) 24%, var(--border)); background: var(--danger-soft); color: var(--danger); }
</style>
