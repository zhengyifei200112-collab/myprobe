<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { DsButton, DsDialog } from './design-system'
import { loadNodes, reorderNodes, type NodeOrderEntry } from './admin-api'
import type { NodeMetadata } from './types'

const props = defineProps<{ nodes: NodeMetadata[] }>()
const emit = defineEmits<{ saved: [] }>()
const open = ref(false)
const busy = ref(false)
const expected = ref<NodeOrderEntry[]>([])
const ordered = ref<NodeMetadata[]>([])
const error = ref('')
const announcement = ref('')
const list = ref<HTMLOListElement>()
const errorMessage = ref<HTMLElement>()
const changed = computed(() => ordered.value.some((node, index) => node.id !== expected.value[index]?.id))
function reset(nodes: NodeMetadata[]) {
  expected.value = nodes.map(node => ({ id: node.id, sort_order: node.sort_order }))
  ordered.value = nodes.map(node => ({ ...node }))
  error.value = ''; announcement.value = ''
}
function show() { reset(props.nodes); open.value = true }
async function reload() {
  busy.value = true
  try { reset((await loadNodes()).nodes); announcement.value = '已载入最新顺序，请重新调整。' }
  catch (value) { error.value = value instanceof Error ? value.message : '载入失败' }
  finally {
    busy.value = false
    await nextTick()
    if (error.value) errorMessage.value?.focus()
    else list.value?.querySelector<HTMLElement>('li')?.focus()
  }
}
async function move(index: number, delta: number, event: Event) {
  const destination = index + delta
  if (busy.value || destination < 0 || destination >= ordered.value.length) return
  const node = ordered.value[index]!
  const action = (event.target as HTMLElement).closest<HTMLButtonElement>('button')?.dataset.move
  const next = [...ordered.value]
  next.splice(index, 1); next.splice(destination, 0, node)
  ordered.value = next
  announcement.value = `${node.name} 已移至第 ${destination + 1} 位，共 ${next.length} 个节点。`
  await nextTick()
  const row = list.value?.querySelector<HTMLElement>(`[data-node-id="${CSS.escape(node.id)}"]`)
  const button = action ? row?.querySelector<HTMLButtonElement>(`button[data-move="${action}"]`) : undefined
  if (button && !button.disabled) button.focus()
  else row?.focus()
}
async function save() {
  if (!changed.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    await reorderNodes(expected.value, ordered.value.map(node => node.id))
    open.value = false; emit('saved')
  } catch (value) { error.value = value instanceof Error ? value.message : '保存失败，请重试或重新载入。' }
  finally {
    busy.value = false
    await nextTick()
    if (error.value) errorMessage.value?.focus()
  }
}
</script>

<template>
  <DsButton :disabled="nodes.length < 2 || nodes.length > 1000" @click="show">调整节点顺序</DsButton>
  <span v-if="nodes.length > 1000" class="order-help">超过 1000 个节点时请使用整数排序编辑。</span>
  <DsDialog :open="open" title="调整节点顺序" description="上移、下移后统一保存；包含隐藏节点。也可聚焦节点后按 Alt + ↑ / ↓。" :dismissible="!busy" @close="open = false">
    <p class="order-help">保存后按此顺序展示，原排序数字将连续编号。取消不会修改任何节点。公开面板刷新后可查看完整新顺序。</p>
    <p v-if="ordered.length > 1000" role="alert">节点超过 1000 个，请使用原有整数排序编辑。</p>
    <p v-if="error" ref="errorMessage" role="alert" tabindex="-1" class="order-error">{{ error }} 响应中断可原样重试；发生冲突请重新载入。</p>
    <p role="status" class="order-status" aria-live="polite">{{ announcement || `共 ${ordered.length} 个节点，尚未保存。` }}</p>
    <ol ref="list" class="order-list" aria-label="节点展示顺序">
      <li v-for="(node, index) in ordered" :key="node.id" :data-node-id="node.id" tabindex="0" :aria-label="`${node.name}，第 ${index + 1} 位`" aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown" @keydown.alt.up.prevent="move(index, -1, $event)" @keydown.alt.down.prevent="move(index, 1, $event)">
        <span class="order-position" aria-hidden="true">{{ index + 1 }}</span>
        <span class="order-name">{{ node.name }}<small v-if="node.hidden">隐藏</small></span>
        <div class="order-actions">
          <DsButton size="small" data-move="up" :aria-label="`上移 ${node.name}`" :disabled="busy || index === 0" @click="move(index, -1, $event)">上移</DsButton>
          <DsButton size="small" data-move="down" :aria-label="`下移 ${node.name}`" :disabled="busy || index === ordered.length - 1" @click="move(index, 1, $event)">下移</DsButton>
        </div>
      </li>
    </ol>
    <template #footer>
      <DsButton :disabled="busy" @click="reload">重新载入</DsButton>
      <DsButton :disabled="busy" @click="open = false">取消</DsButton>
      <DsButton variant="primary" :disabled="busy || !changed || ordered.length > 1000" :loading="busy" @click="save">保存顺序</DsButton>
    </template>
  </DsDialog>
</template>

<style scoped>
.order-help, .order-status { font-size: var(--font-size-sm); color: var(--text-secondary); line-height: var(--line-height-body); }
.order-status { min-height: 2.5em; }
.order-error { color: var(--danger); overflow-wrap: anywhere; }
.order-list { max-height: min(26rem, 42vh); overflow: auto; scrollbar-gutter: stable; margin: 0; padding: 3px; list-style: none; }
.order-list li { display: grid; grid-template-columns: 2ch minmax(0, 1fr) auto; align-items: center; gap: var(--space-3); padding: var(--space-4) var(--space-2); border-bottom: 1px solid var(--border); border-radius: var(--radius-control); }
.order-position { color: var(--text-tertiary); font-variant-numeric: tabular-nums; }
.order-name { overflow-wrap: anywhere; font-weight: var(--font-weight-medium); }
.order-name small { display: block; font-size: var(--font-size-xs); font-weight: normal; color: var(--text-tertiary); }
.order-actions { display: flex; gap: var(--space-2); }
@media (max-width: 42.5rem) {
  .order-list li { grid-template-columns: 2ch minmax(0, 1fr); }
  .order-actions { grid-column: 2; }
}
</style>
