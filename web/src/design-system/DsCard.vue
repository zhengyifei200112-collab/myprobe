<script setup lang="ts">
withDefaults(defineProps<{ as?: string; padding?: 'none' | 'small' | 'medium' | 'large'; interactive?: boolean }>(), { as: 'section', padding: 'medium', interactive: false })
</script>

<template>
  <component :is="as" class="ds-card" :class="[`ds-card--${padding}`, { 'ds-card--interactive': interactive }]">
    <slot></slot>
  </component>
</template>

<style scoped>
.ds-card { border: 1px solid var(--border); border-radius: var(--radius-card); background: var(--surface); box-shadow: var(--shadow-sm); color: var(--text-primary); }
.ds-card--none { padding: 0; }.ds-card--small { padding: var(--space-4); }.ds-card--medium { padding: var(--space-6); }.ds-card--large { padding: var(--space-8); }
.ds-card--interactive { transition: transform var(--duration-normal) var(--ease-standard), border-color var(--duration-normal) var(--ease-standard), box-shadow var(--duration-normal) var(--ease-standard); }
.ds-card--interactive:hover { transform: translateY(-2px); border-color: var(--border-strong); box-shadow: var(--shadow-md); }
@media (max-width: 680px) { .ds-card--large { padding: var(--space-6); } }
</style>
