<script setup lang="ts">
import { ref, useId } from 'vue'

type TabItem = { value: string; label: string; disabled?: boolean; panelId?: string }
const props = defineProps<{ modelValue: string; items: TabItem[]; label: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const root = ref<HTMLElement>()
const id = useId()

function move(event: KeyboardEvent, index: number) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const enabled = props.items.map((item, itemIndex) => ({ item, itemIndex })).filter(({ item }) => !item.disabled)
  const current = enabled.findIndex(({ itemIndex }) => itemIndex === index)
  const next = event.key === 'Home' ? enabled[0] : event.key === 'End' ? enabled.at(-1) : enabled[(current + (event.key === 'ArrowRight' ? 1 : -1) + enabled.length) % enabled.length]
  if (!next) return
  emit('update:modelValue', next.item.value)
  root.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next.itemIndex]?.focus()
}
</script>

<template><div ref="root" class="ds-tabs" role="tablist" :aria-label="label"><button v-for="(item, index) in items" :id="`${id}-${item.value}-tab`" :key="item.value" type="button" role="tab" :aria-selected="modelValue === item.value" :aria-controls="item.panelId" :tabindex="modelValue === item.value ? 0 : -1" :disabled="item.disabled" @click="$emit('update:modelValue', item.value)" @keydown="move($event, index)">{{ item.label }}</button></div></template>

<style scoped>
.ds-tabs { padding: .25rem; display: inline-flex; gap: .2rem; overflow-x: auto; border-radius: var(--radius-control); background: var(--surface-secondary); scrollbar-width: none; }.ds-tabs::-webkit-scrollbar { display: none; }
.ds-tabs button { min-height: 2.25rem; padding: 0 var(--space-4); flex: 0 0 auto; border: 0; border-radius: .625rem; background: transparent; color: var(--text-secondary); cursor: pointer; font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); transition: background var(--duration-fast) var(--ease-standard), color var(--duration-fast) var(--ease-standard), box-shadow var(--duration-fast) var(--ease-standard); }
.ds-tabs button[aria-selected='true'] { background: var(--surface-solid); color: var(--text-primary); box-shadow: var(--shadow-xs); }
</style>
