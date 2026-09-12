<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId } from 'vue'

withDefaults(defineProps<{ label: string; align?: 'start' | 'end' }>(), { align: 'end' })
const open = ref(false)
const root = ref<HTMLElement>()
const menuID = useId()

function close() {
  open.value = false
  document.removeEventListener('pointerdown', outside)
}
async function toggle() {
  open.value = !open.value
  if (open.value) {
    document.addEventListener('pointerdown', outside)
    await nextTick()
  } else document.removeEventListener('pointerdown', outside)
}
function outside(event: PointerEvent) { if (!root.value?.contains(event.target as Node)) close() }
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape') { close(); root.value?.querySelector<HTMLElement>('.ds-dropdown__trigger')?.focus() }
  if (event.key === 'ArrowDown' && open.value) { event.preventDefault(); root.value?.querySelector<HTMLElement>('[role="menuitem"]')?.focus() }
}
onBeforeUnmount(() => document.removeEventListener('pointerdown', outside))
defineExpose({ close })
</script>

<template>
  <div ref="root" class="ds-dropdown" @keydown="keydown">
    <button type="button" class="ds-dropdown__trigger" aria-haspopup="menu" :aria-expanded="open" :aria-controls="menuID" @click="toggle"><slot name="trigger">{{ label }}</slot></button>
    <Transition name="ds-menu"><div v-if="open" :id="menuID" class="ds-dropdown__menu" :class="`ds-dropdown__menu--${align}`" role="menu" @click="close"><slot></slot></div></Transition>
  </div>
</template>

<style scoped>
.ds-dropdown { position: relative; display: inline-flex; }
.ds-dropdown__trigger { min-width: var(--touch-target); min-height: var(--touch-target); padding: 0 var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-solid); color: var(--text-primary); cursor: pointer; }
.ds-dropdown__menu { position: absolute; top: calc(100% + var(--space-2)); z-index: var(--z-dropdown); min-width: 12rem; padding: var(--space-2); border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--surface-solid); box-shadow: var(--shadow-md); }
.ds-dropdown__menu--start { left: 0; }.ds-dropdown__menu--end { right: 0; }
.ds-dropdown__menu :deep([role='menuitem']) { width: 100%; min-height: 2.5rem; padding: 0 var(--space-3); display: flex; align-items: center; border: 0; border-radius: .625rem; background: transparent; color: var(--text-primary); cursor: pointer; text-align: left; }
.ds-dropdown__menu :deep([role='menuitem']:hover), .ds-dropdown__menu :deep([role='menuitem']:focus-visible) { background: var(--control-hover); }
.ds-menu-enter-active, .ds-menu-leave-active { transition: opacity var(--duration-fast) var(--ease-standard), transform var(--duration-fast) var(--ease-standard); transform-origin: top; }
.ds-menu-enter-from, .ds-menu-leave-to { opacity: 0; transform: translateY(-.25rem); }
</style>
