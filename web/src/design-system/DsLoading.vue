<script setup lang="ts">
withDefaults(defineProps<{ label?: string; variant?: 'spinner' | 'skeleton'; lines?: number }>(), { label: '正在加载', variant: 'spinner', lines: 3 })
</script>

<template><div class="ds-loading" :class="`ds-loading--${variant}`" role="status" :aria-label="label"><template v-if="variant === 'spinner'"><span aria-hidden="true"></span><b>{{ label }}</b></template><template v-else><i v-for="line in lines" :key="line" aria-hidden="true"></i><span class="ds-visually-hidden">{{ label }}</span></template></div></template>

<style scoped>
.ds-loading--spinner { min-height: 8rem; display: grid; place-content: center; justify-items: center; gap: var(--space-3); color: var(--text-secondary); }.ds-loading--spinner > span { width: 1.5rem; height: 1.5rem; border: 2px solid var(--surface-tertiary); border-top-color: var(--accent); border-radius: 50%; animation: ds-loading-spin .75s linear infinite; }.ds-loading--spinner b { font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); }
.ds-loading--skeleton { display: grid; gap: var(--space-3); }.ds-loading--skeleton i { height: .875rem; border-radius: var(--radius-round); background: var(--surface-tertiary); animation: ds-loading-pulse 1.5s ease-in-out infinite; }.ds-loading--skeleton i:last-of-type { width: 62%; }
@keyframes ds-loading-spin { to { transform: rotate(360deg); } } @keyframes ds-loading-pulse { 50% { opacity: .52; } }
</style>
