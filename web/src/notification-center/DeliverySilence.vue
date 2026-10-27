<script setup lang="ts">
import { ref, watch } from 'vue'
import { loadDeliverySilences, type SilencePage } from '../admin-api'
import { DsButton } from '../design-system'
import SilenceRecords from './SilenceRecords.vue'
const props = defineProps<{ id: string }>()
const view = ref<SilencePage | null>(null), busy = ref(false), error = ref('')
let generation = 0
async function load(more = false) {
  const current = ++generation; busy.value = true; error.value = ''
  try { const page = await loadDeliverySilences(props.id, more ? view.value?.next_cursor || '' : ''); if (current === generation) view.value = more && view.value ? { ...page, silences: [...view.value.silences,...page.silences] } : page }
  catch(e) { if(current === generation) error.value = e instanceof Error ? e.message : '静默关联加载失败' }
  finally { if(current === generation) busy.value = false }
}
watch(() => props.id, () => { view.value = null; void load() }, { immediate: true })
</script>
<template><section aria-label="通知静默关联"><p v-if="busy" role="status">正在加载静默关联…</p><p v-if="error" role="alert">{{ error }}</p><SilenceRecords v-if="view" :records="view.silences" /><p v-if="view && !view.silences.length">没有记录此通知的静默关联。</p><DsButton size="small" :disabled="busy" @click="load()">刷新静默关联</DsButton><DsButton v-if="view?.next_cursor" :disabled="busy" @click="load(true)">加载更多静默关联</DsButton></section></template>
