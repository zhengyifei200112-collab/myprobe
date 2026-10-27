<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { DsDialog } from '../design-system'
import type { ServiceAlertBundle, ServiceAlertDestination, ServiceAlertImportMapping } from '../admin-api'

const props = defineProps<{ bundle: ServiceAlertBundle; services: ServiceAlertDestination[]; channels: ServiceAlertDestination[]; mapping: ServiceAlertImportMapping; disabled: boolean }>()
const emit = defineEmits<{ select: [kind: 'services' | 'channels', source: string, destination: string] }>()
const size = 25
const serviceSearch = ref(''), channelSearch = ref(''), serviceMode = ref('all'), channelMode = ref('all')
const servicePage = ref(1), channelPage = ref(1)
const picker = ref<{ kind: 'services' | 'channels'; source: string; name: string } | null>(null)
const destinationSearch = ref(''), destinationPage = ref(1)
const serviceByID = computed(() => new Map(props.services.map(v => [v.id, v])))
const channelByID = computed(() => new Map(props.channels.map(v => [v.id, v])))
function matches(name: string, id: string, search: string) { return `${name} ${id}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()) }
function modeMatch(id: string, kind: 'services' | 'channels', mode: string) { return mode === 'all' || (mode === 'mapped') === !!props.mapping[kind][id] }
const sourceServices = computed(() => props.bundle.alerts.filter(v => matches(v.name, v.source_id, serviceSearch.value) && modeMatch(v.source_id, 'services', serviceMode.value)))
const sourceChannels = computed(() => props.bundle.channels.filter(v => matches(v.name, v.id, channelSearch.value) && modeMatch(v.id, 'channels', channelMode.value)))
const servicePages = computed(() => Math.max(1, Math.ceil(sourceServices.value.length / size))), channelPages = computed(() => Math.max(1, Math.ceil(sourceChannels.value.length / size)))
const visibleServices = computed(() => sourceServices.value.slice((servicePage.value - 1) * size, servicePage.value * size))
const visibleChannels = computed(() => sourceChannels.value.slice((channelPage.value - 1) * size, channelPage.value * size))
const mappedServices = computed(() => props.bundle.alerts.filter(v => !!props.mapping.services[v.source_id]).length)
const mappedChannels = computed(() => props.bundle.channels.filter(v => !!props.mapping.channels[v.id]).length)
const candidates = computed(() => (picker.value?.kind === 'services' ? props.services : props.channels).filter(v => matches(`${v.name} ${v.provider || ''}`, v.id, destinationSearch.value)))
const destinationPages = computed(() => Math.max(1, Math.ceil(candidates.value.length / size)))
const visibleCandidates = computed(() => candidates.value.slice((destinationPage.value - 1) * size, destinationPage.value * size))
const usedServices = computed(() => new Set(Object.entries(props.mapping.services).filter(([source]) => source !== picker.value?.source).map(([, id]) => id)))
function selected(kind: 'services' | 'channels', source: string) {
  const id = props.mapping[kind][source]
  if (!id) return '尚未选择'
  const value = (kind === 'services' ? serviceByID.value : channelByID.value).get(id)
  return value ? `${value.name} · ${id}${value.provider ? ` · ${value.provider}` : ''}${value.enabled ? '' : '（停用）'}` : `待核对目标 ${id}`
}
function open(kind: 'services' | 'channels', source: string, name: string) { if (props.disabled) return; destinationSearch.value = ''; destinationPage.value = 1; picker.value = { kind, source, name } }
function choose(value: ServiceAlertDestination) {
  if (!picker.value || props.disabled || (picker.value.kind === 'services' && usedServices.value.has(value.id))) return
  emit('select', picker.value.kind, picker.value.source, value.id); picker.value = null
}
watch([serviceSearch, serviceMode], () => { servicePage.value = 1 })
watch([channelSearch, channelMode], () => { channelPage.value = 1 })
watch(destinationSearch, () => { destinationPage.value = 1 })
watch(servicePages, count => { servicePage.value = Math.min(servicePage.value, count) })
watch(channelPages, count => { channelPage.value = Math.min(channelPage.value, count) })
watch(() => props.bundle, () => { serviceSearch.value = ''; channelSearch.value = ''; serviceMode.value = 'all'; channelMode.value = 'all'; servicePage.value = 1; channelPage.value = 1; picker.value = null })
</script>

<template>
  <div class="service-alert-mapping">
    <h3>来源 → 当前环境</h3>
    <p>已选服务 {{ mappedServices }} / {{ bundle.alerts.length }}，已选通道 {{ mappedChannels }} / {{ bundle.channels.length }}。分页只改变显示，预览和导入仍覆盖完整文件。</p>
    <fieldset :disabled="disabled" class="mapping-group" aria-label="服务映射">
      <legend>服务映射</legend>
      <div class="mapping-filters"><label>搜索来源服务<input v-model="serviceSearch" maxlength="200" type="search"></label><label>服务选择状态<select v-model="serviceMode"><option value="all">全部</option><option value="unmapped">未选择</option><option value="mapped">已选择</option></select></label></div>
      <div v-for="source in visibleServices" :key="source.source_id" class="mapping-row"><strong>{{ source.name }}</strong><small>来源 {{ source.source_id }}</small><span>{{ selected('services', source.source_id) }}</span><button type="button" class="soft-button" :aria-label="`映射服务 ${source.name}`" @click="open('services', source.source_id, source.name)">选择目标服务</button></div>
      <p v-if="!sourceServices.length">没有匹配的来源服务。</p>
      <div class="mapping-pages"><button type="button" :disabled="servicePage === 1" @click="servicePage--">服务映射上一页</button><span>服务第 {{ servicePage }} / {{ servicePages }} 页 · {{ sourceServices.length }} 项</span><button type="button" :disabled="servicePage === servicePages" @click="servicePage++">服务映射下一页</button></div>
    </fieldset>
    <fieldset :disabled="disabled" class="mapping-group" aria-label="通道映射">
      <legend>通道映射</legend>
      <div class="mapping-filters"><label>搜索来源通道<input v-model="channelSearch" maxlength="200" type="search"></label><label>通道选择状态<select v-model="channelMode"><option value="all">全部</option><option value="unmapped">未选择</option><option value="mapped">已选择</option></select></label></div>
      <div v-for="source in visibleChannels" :key="source.id" class="mapping-row"><strong>{{ source.name }} · {{ source.provider }}</strong><small>来源 {{ source.id }}</small><span>{{ selected('channels', source.id) }}</span><button type="button" class="soft-button" :aria-label="`映射通道 ${source.name}`" @click="open('channels', source.id, source.name)">选择目标通道</button></div>
      <p v-if="!sourceChannels.length">没有匹配的来源通道。</p>
      <div class="mapping-pages"><button type="button" :disabled="channelPage === 1" @click="channelPage--">通道映射上一页</button><span>通道第 {{ channelPage }} / {{ channelPages }} 页 · {{ sourceChannels.length }} 项</span><button type="button" :disabled="channelPage === channelPages" @click="channelPage++">通道映射下一页</button></div>
    </fieldset>
    <DsDialog :open="!!picker" :title="picker?.kind === 'services' ? '选择目标服务' : '选择目标通道'" :description="picker ? `来源：${picker.name}（${picker.source}）` : ''" @close="picker = null">
      <div class="destination-picker"><label>搜索目标名称或 ID<input v-model="destinationSearch" maxlength="200" type="search" autofocus></label><p>匹配 {{ candidates.length }} 项；同名对象请核对 ID。停用状态不会因迁移而改变。</p>
        <div v-for="value in visibleCandidates" :key="value.id" class="destination-row"><strong>{{ value.name }}{{ value.provider ? ` · ${value.provider}` : '' }}</strong><small>{{ value.id }}</small><span>{{ value.enabled ? '已启用' : '已停用' }}</span><span v-if="picker?.kind === 'services' && usedServices.has(value.id)">已分配给其他来源</span><button type="button" class="soft-button" :disabled="disabled || (picker?.kind === 'services' && usedServices.has(value.id))" :aria-label="`选择 ${value.name}（${value.id}）`" @click="choose(value)">选择此目标</button></div>
        <p v-if="!candidates.length">没有匹配的目标。</p><div class="mapping-pages"><button type="button" :disabled="destinationPage === 1" @click="destinationPage--">目标上一页</button><span>目标第 {{ destinationPage }} / {{ destinationPages }} 页</span><button type="button" :disabled="destinationPage === destinationPages" @click="destinationPage++">目标下一页</button></div>
      </div>
    </DsDialog>
  </div>
</template>

<style scoped>
.service-alert-mapping,.mapping-group,.destination-picker { display:grid; grid-template-columns:minmax(0,1fr); gap:14px; min-width:0; }
.service-alert-mapping h3,.service-alert-mapping p { margin:0; }
.mapping-group { border:0; padding:0; margin:0; }
.mapping-group legend { font-weight:600; padding:0 0 12px; }
.mapping-filters { display:grid; grid-template-columns:minmax(0,2fr) minmax(0,1fr); gap:12px; }
.mapping-filters label,.destination-picker label { display:grid; gap:8px; min-width:0; }
.mapping-filters input,.mapping-filters select,.destination-picker input { width:100%; box-sizing:border-box; min-width:0; min-height:40px; padding:8px 10px; color:var(--text); background:var(--surface); border:1px solid var(--border); border-radius:8px; }
.mapping-row,.destination-row { display:grid; gap:8px; padding:14px; border:1px solid var(--border); border-radius:12px; min-width:0; overflow-wrap:anywhere; }
.mapping-row small,.destination-row small { color:var(--text-secondary); }
.mapping-row button,.destination-row button { justify-self:start; }
.mapping-pages { display:flex; flex-wrap:wrap; align-items:center; gap:10px; }
.mapping-pages button { min-height:40px; }
@media(max-width:600px) { .mapping-filters { grid-template-columns:minmax(0,1fr); } }
</style>
