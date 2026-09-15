<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  description?: string
  presentation?: 'dialog' | 'sheet'
  closeLabel?: string
  dismissible?: boolean
}>(), { description: '', presentation: 'dialog', closeLabel: '关闭', dismissible: true })
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement>()
const titleID = useId()
const descriptionID = useId()
let previousFocus: HTMLElement | null = null
let locked = false

const focusable = () => Array.from(panel.value?.querySelectorAll<HTMLElement>('button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])') ?? [])

function close() {
  if (props.dismissible) emit('close')
}

function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.dismissible) {
    event.preventDefault()
    emit('close')
    return
  }
  if (event.key !== 'Tab') return
  const items = focusable()
  if (!items.length) {
    event.preventDefault()
    panel.value?.focus()
    return
  }
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.open, async (open) => {
  if (open) {
    if (!locked) { document.body.classList.add('has-modal'); locked = true }
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    await nextTick()
    ;(panel.value?.querySelector<HTMLElement>('[autofocus]') ?? focusable()[0] ?? panel.value)?.focus()
  } else {
    if (locked) { document.body.classList.remove('has-modal'); locked = false }
    previousFocus?.focus()
    previousFocus = null
  }
}, { immediate: true })

onBeforeUnmount(() => { previousFocus?.focus(); if (locked) document.body.classList.remove('has-modal') })
</script>

<template>
  <Teleport to="body">
    <Transition name="ds-dialog">
      <div v-if="open" class="ds-dialog__overlay" :class="`ds-dialog__overlay--${presentation}`" @mousedown.self="close" @keydown="keydown">
        <section ref="panel" class="ds-dialog__panel" :class="`ds-dialog__panel--${presentation}`" role="dialog" aria-modal="true" :aria-labelledby="titleID" :aria-describedby="description ? descriptionID : undefined" tabindex="-1">
          <header class="ds-dialog__header">
            <div><h2 :id="titleID">{{ title }}</h2><p v-if="description" :id="descriptionID">{{ description }}</p></div>
            <button v-if="dismissible" type="button" class="ds-dialog__close" :aria-label="closeLabel" @click="emit('close')">×</button>
          </header>
          <div class="ds-dialog__body"><slot></slot></div>
          <footer v-if="$slots.footer" class="ds-dialog__footer"><slot name="footer"></slot></footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.ds-dialog__overlay { position: fixed; inset: 0; z-index: var(--z-overlay); padding: var(--space-6); display: grid; place-items: center; overflow: auto; background: var(--overlay); }
.ds-dialog__panel { width: min(38rem, 100%); max-height: min(52rem, calc(100vh - 3rem)); overflow: auto; border: 1px solid var(--border); border-radius: var(--radius-dialog); background: var(--surface-solid); box-shadow: var(--shadow-dialog); outline: 0; }
.ds-dialog__header { padding: var(--space-6) var(--space-6) var(--space-4); display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-4); }
.ds-dialog__header h2 { margin: 0; font-size: var(--font-size-xl); line-height: var(--line-height-tight); }
.ds-dialog__header p { margin: var(--space-2) 0 0; color: var(--text-secondary); font-size: var(--font-size-sm); }
.ds-dialog__close { width: var(--touch-target); height: var(--touch-target); flex: 0 0 auto; border: 0; border-radius: 50%; background: var(--surface-secondary); color: var(--text-secondary); cursor: pointer; font-size: 1.35rem; }
.ds-dialog__body { padding: 0 var(--space-6) var(--space-6); }
.ds-dialog__footer { padding: var(--space-4) var(--space-6) var(--space-6); display: flex; justify-content: flex-end; gap: var(--space-3); border-top: 1px solid var(--border); }
.ds-dialog__overlay--sheet { place-items: stretch end; padding: 0; }
.ds-dialog__panel--sheet { width: min(30rem, 100%); max-height: 100vh; height: 100%; border-radius: var(--radius-dialog) 0 0 var(--radius-dialog); }
.ds-dialog-enter-active, .ds-dialog-leave-active { transition: opacity var(--duration-normal) var(--ease-standard); }
.ds-dialog-enter-active .ds-dialog__panel, .ds-dialog-leave-active .ds-dialog__panel { transition: transform var(--duration-normal) var(--ease-emphasized), opacity var(--duration-normal) var(--ease-standard); }
.ds-dialog-enter-from, .ds-dialog-leave-to { opacity: 0; }
.ds-dialog-enter-from .ds-dialog__panel, .ds-dialog-leave-to .ds-dialog__panel { opacity: 0; transform: translateY(.5rem) scale(.985); }
.ds-dialog-enter-from .ds-dialog__panel--sheet, .ds-dialog-leave-to .ds-dialog__panel--sheet { transform: translateX(1rem); }
@media (max-width: 680px) { .ds-dialog__overlay { padding: var(--space-3); place-items: end center; }.ds-dialog__panel { max-height: calc(100vh - var(--space-6)); border-radius: var(--radius-dialog); }.ds-dialog__panel--sheet { width: 100%; height: auto; max-height: 92vh; border-radius: var(--radius-dialog) var(--radius-dialog) 0 0; }.ds-dialog__header, .ds-dialog__body, .ds-dialog__footer { padding-left: var(--space-5); padding-right: var(--space-5); } }
</style>
