<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { formatQuantity, parseQuantity, type Unit } from './units'

const props = withDefaults(defineProps<{ modelValue?: number; units: Unit[]; label: string; min?: number; max?: number; optional?: boolean; describedBy?: string }>(), { min: 0, max: Number.MAX_SAFE_INTEGER, optional: false })
const emit = defineEmits<{ 'update:modelValue': [value: number | undefined] }>()
const factor = ref(1)
const text = ref('')
const problem = ref('')
const switchNotice = ref('')
const input = ref<HTMLInputElement>()
const unitKey = computed(() => props.units.map(unit => unit.factor).join(','))
const selectedUnit = computed(() => props.units.find(unit => unit.factor === factor.value))

function sync() {
  problem.value = ''
  text.value = props.modelValue == null ? '' : String(props.modelValue)
  try {
    text.value = props.modelValue == null ? '' : formatQuantity(props.modelValue, factor.value)
    if (props.modelValue != null) checkRange(props.modelValue)
  }
  catch (error) { problem.value = (error as Error).message }
}
watch(() => props.modelValue, sync, { immediate: true })
watch(unitKey, () => { factor.value = props.units[0]!.factor; sync() }, { immediate: true })
watch([problem, input], () => input.value?.setCustomValidity(problem.value), { flush: 'post' })

function checkRange(value: number) {
  if (value < props.min || value > props.max) throw new Error(`范围为 ${props.min}—${props.max} 个最小单位。`)
}

function edit(event: Event) {
  text.value = (event.target as HTMLInputElement).value
  switchNotice.value = ''
  try {
    if (!text.value.trim() && props.optional) { emit('update:modelValue', undefined); problem.value = ''; return }
    const value = parseQuantity(text.value, factor.value)
    checkRange(value)
    problem.value = ''
    emit('update:modelValue', value)
  } catch (error) { problem.value = (error as Error).message }
}

function changeUnit(event: Event) {
  const select = event.target as HTMLSelectElement
  try {
    if (problem.value) throw new Error('请先修正数值，再切换单位。')
    const next = Number(select.value)
    const formatted = props.modelValue == null ? '' : formatQuantity(props.modelValue, next)
    factor.value = next
    text.value = formatted
    switchNotice.value = ''
  } catch (error) { select.value = String(factor.value); switchNotice.value = (error as Error).message }
}
</script>

<template>
  <div class="quantity-field">
    <label><span>{{ label }}</span><input ref="input" :aria-label="label" :value="text" inputmode="decimal" :required="!optional" :aria-describedby="describedBy" :aria-invalid="!!problem" @input="edit"></label>
    <label v-if="units.length > 1"><span>单位</span><select :aria-label="`${label}单位`" :value="factor" @change="changeUnit"><option v-for="unit in units" :key="unit.label" :value="unit.factor">{{ unit.label }}</option></select></label>
    <small v-else>{{ units[0]?.label }}</small>
    <small v-if="selectedUnit?.hint" class="quantity-hint">{{ selectedUnit.hint }}</small>
    <small v-if="problem || switchNotice" class="quantity-message" role="status">{{ problem || switchNotice }}</small>
  </div>
</template>

<style scoped>
.quantity-field{display:flex;flex-wrap:wrap;gap:8px;align-items:end;min-width:0}.quantity-field>label{flex:1;min-width:0;display:grid;gap:6px}.quantity-field input,.quantity-field select{width:100%;min-width:0;box-sizing:border-box;padding:10px 12px;border:1px solid var(--border);border-radius:10px;background:var(--surface-subtle);color:inherit;font:inherit}.quantity-field input:focus-visible,.quantity-field select:focus-visible{outline:2px solid var(--blue);outline-offset:2px}.quantity-field>label:nth-child(2){flex:0 0 100px}.quantity-field>small{color:var(--muted)}.quantity-hint{flex-basis:100%;overflow-wrap:anywhere}.quantity-message{flex-basis:100%;color:var(--red)!important}
</style>
