<script setup lang="ts">
import { useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string | number; label?: string; help?: string; error?: string; disabled?: boolean; required?: boolean }>(), { modelValue: '', label: '', help: '', error: '', disabled: false, required: false })
defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const generatedID = useId()
const selectID = String(attrs.id ?? generatedID)
</script>

<template>
  <label class="ds-field" :for="selectID">
    <span v-if="label" class="ds-field__label">{{ label }}<span v-if="required" aria-hidden="true"> *</span></span>
    <span class="ds-select-wrap">
      <select v-bind="attrs" :id="selectID" class="ds-select" :value="modelValue" :disabled="disabled" :required="required" :aria-invalid="error ? 'true' : undefined" :aria-describedby="error ? `${selectID}-error` : help ? `${selectID}-help` : undefined" @change="$emit('update:modelValue', ($event.target as HTMLSelectElement).value)"><slot></slot></select>
    </span>
    <span v-if="error" :id="`${selectID}-error`" class="ds-field__message ds-field__message--error">{{ error }}</span>
    <span v-else-if="help" :id="`${selectID}-help`" class="ds-field__message">{{ help }}</span>
  </label>
</template>

<style scoped>
.ds-field { display: grid; gap: var(--space-2); color: var(--text-primary); }
.ds-field__label { font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); }
.ds-select-wrap { position: relative; }
.ds-select-wrap::after { content: ''; position: absolute; right: var(--space-4); top: 50%; width: .45rem; height: .45rem; border-right: 1.5px solid var(--text-tertiary); border-bottom: 1.5px solid var(--text-tertiary); transform: translateY(-70%) rotate(45deg); pointer-events: none; }
.ds-select { width: 100%; min-height: var(--control-height); padding: 0 2.5rem 0 var(--space-4); appearance: none; border: 1px solid var(--border-strong); border-radius: var(--radius-control); outline: 0; background: var(--surface-solid); color: var(--text-primary); }
.ds-select:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--focus-ring); }
.ds-field__message { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.ds-field__message--error { color: var(--danger); }
</style>
