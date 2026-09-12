<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string; compact?: boolean }>(), { description: '', compact: false })
</script>

<template><section class="ds-empty" :class="{ 'ds-empty--compact': compact }"><div v-if="$slots.icon" class="ds-empty__icon" aria-hidden="true"><slot name="icon"></slot></div><h2>{{ title }}</h2><p v-if="description">{{ description }}</p><div v-if="$slots.action" class="ds-empty__action"><slot name="action"></slot></div></section></template>

<style scoped>
.ds-empty { min-height: 16rem; padding: var(--space-8); display: grid; place-content: center; justify-items: center; text-align: center; }.ds-empty--compact { min-height: 10rem; padding: var(--space-6); }.ds-empty__icon { width: 3.5rem; height: 3.5rem; margin-bottom: var(--space-4); display: grid; place-items: center; border-radius: var(--radius-card); background: var(--surface-secondary); color: var(--accent); }.ds-empty h2 { margin: 0; font-size: var(--font-size-lg); }.ds-empty p { max-width: 28rem; margin: var(--space-2) 0 0; color: var(--text-secondary); font-size: var(--font-size-sm); }.ds-empty__action { margin-top: var(--space-5); }
</style>
