<script setup lang="ts">
import { useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  modelValue?: string | number
  label?: string
  help?: string
  error?: string
  type?: string
  disabled?: boolean
  required?: boolean
}>(), { modelValue: '', label: '', help: '', error: '', type: 'text', disabled: false, required: false })
const emit = defineEmits<{ 'update:modelValue': [value: string | number] }>()
const attrs = useAttrs()
const generatedID = useId()
const inputID = String(attrs.id ?? generatedID)

function update(event: Event) {
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', props.type === 'number' && value !== '' ? Number(value) : value)
}
</script>

<template>
  <label class="ds-field" :for="inputID">
    <span v-if="label" class="ds-field__label">{{ label }}<span v-if="required" aria-hidden="true"> *</span></span>
    <input
      v-bind="attrs"
      :id="inputID"
      class="ds-input"
      :class="{ 'ds-input--error': error }"
      :value="modelValue"
      :type="type"
      :disabled="disabled"
      :required="required"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="error ? `${inputID}-error` : help ? `${inputID}-help` : undefined"
      @input="update"
    >
    <span v-if="error" :id="`${inputID}-error`" class="ds-field__message ds-field__message--error">{{ error }}</span>
    <span v-else-if="help" :id="`${inputID}-help`" class="ds-field__message">{{ help }}</span>
  </label>
</template>

<style scoped>
.ds-field { display: grid; gap: var(--space-2); color: var(--text-primary); }
.ds-field__label { font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); }
.ds-input { width: 100%; min-height: var(--control-height); padding: 0 var(--space-4); border: 1px solid var(--border-strong); border-radius: var(--radius-control); outline: 0; background: var(--surface-solid); color: var(--text-primary); transition: border-color var(--duration-fast) var(--ease-standard), box-shadow var(--duration-fast) var(--ease-standard); }
.ds-input::placeholder { color: var(--text-tertiary); }
.ds-input:hover:not(:disabled) { border-color: color-mix(in srgb, var(--text-tertiary) 45%, var(--border)); }
.ds-input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--focus-ring); }
.ds-input--error { border-color: var(--danger); }
.ds-field__message { color: var(--text-tertiary); font-size: var(--font-size-xs); line-height: var(--line-height-body); }
.ds-field__message--error { color: var(--danger); }
</style>
