<script setup lang="ts">
import { onBeforeUnmount, watch } from 'vue'

const props = withDefaults(defineProps<{ open: boolean; title?: string; message: string; tone?: 'neutral' | 'success' | 'warning' | 'danger'; duration?: number }>(), { title: '', tone: 'neutral', duration: 5000 })
const emit = defineEmits<{ dismiss: [] }>()
let timer: number | undefined
watch(() => props.open, (open) => {
  if (timer !== undefined) window.clearTimeout(timer)
  if (open && props.duration > 0) timer = window.setTimeout(() => emit('dismiss'), props.duration)
}, { immediate: true })
onBeforeUnmount(() => { if (timer !== undefined) window.clearTimeout(timer) })
</script>

<template><Teleport to="body"><Transition name="ds-toast"><section v-if="open" class="ds-toast" :class="`ds-toast--${tone}`" :role="tone === 'danger' ? 'alert' : 'status'" :aria-live="tone === 'danger' ? 'assertive' : 'polite'"><div><strong v-if="title">{{ title }}</strong><p>{{ message }}</p></div><button type="button" aria-label="关闭通知" @click="$emit('dismiss')">×</button></section></Transition></Teleport></template>

<style scoped>
.ds-toast { position: fixed; right: var(--space-6); bottom: var(--space-6); z-index: var(--z-toast); width: min(24rem, calc(100vw - 2rem)); padding: var(--space-4); display: flex; align-items: flex-start; gap: var(--space-4); border: 1px solid var(--border); border-radius: var(--radius-button); background: var(--surface-solid); box-shadow: var(--shadow-md); color: var(--text-primary); }
.ds-toast::before { content: ''; width: .5rem; height: .5rem; margin-top: .45rem; flex: 0 0 auto; border-radius: 50%; background: var(--text-tertiary); }
.ds-toast--success::before { background: var(--success); }.ds-toast--warning::before { background: var(--warning); }.ds-toast--danger::before { background: var(--danger); }
.ds-toast div { min-width: 0; flex: 1; }.ds-toast strong { display: block; font-size: var(--font-size-sm); }.ds-toast p { margin: .2rem 0 0; color: var(--text-secondary); font-size: var(--font-size-sm); }.ds-toast button { width: 2rem; height: 2rem; border: 0; border-radius: 50%; background: transparent; color: var(--text-tertiary); cursor: pointer; }
.ds-toast-enter-active, .ds-toast-leave-active { transition: opacity var(--duration-normal) var(--ease-standard), transform var(--duration-normal) var(--ease-emphasized); }.ds-toast-enter-from, .ds-toast-leave-to { opacity: 0; transform: translateY(.5rem); }
@media (max-width: 680px) { .ds-toast { right: var(--space-4); bottom: var(--space-4); } }
</style>
