<script setup lang="ts">
withDefaults(defineProps<{ status: 'online' | 'offline' | 'warning' | 'neutral'; label: string; pulse?: boolean }>(), { pulse: false })
</script>

<template><span class="ds-status" :class="[`ds-status--${status}`, { 'ds-status--pulse': pulse }]" role="status"><i aria-hidden="true"></i><span>{{ label }}</span></span></template>

<style scoped>
.ds-status { display: inline-flex; align-items: center; gap: .45rem; color: var(--text-secondary); font-size: var(--font-size-xs); font-weight: var(--font-weight-semibold); white-space: nowrap; }.ds-status i { width: .5rem; height: .5rem; border-radius: 50%; background: var(--text-tertiary); box-shadow: 0 0 0 .22rem color-mix(in srgb, currentColor 10%, transparent); }.ds-status--online { color: var(--success); }.ds-status--offline { color: var(--danger); }.ds-status--warning { color: var(--warning); }.ds-status--pulse i { animation: ds-status-pulse 2s ease-in-out infinite; }
@keyframes ds-status-pulse { 50% { box-shadow: 0 0 0 .38rem color-mix(in srgb, currentColor 5%, transparent); } }
</style>
