<script setup lang="ts">
import { useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ modelValue?: string; label?: string; help?: string; error?: string; disabled?: boolean; required?: boolean; rows?: number }>(), { modelValue: '', label: '', help: '', error: '', disabled: false, required: false, rows: 4 })
defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const generatedID = useId()
const textareaID = String(attrs.id ?? generatedID)
</script>

<template>
  <label class="ds-field" :for="textareaID">
    <span v-if="label" class="ds-field__label">{{ label }}<span v-if="required" aria-hidden="true"> *</span></span>
    <textarea v-bind="attrs" :id="textareaID" class="ds-textarea" :class="{ 'ds-textarea--error': error }" :value="modelValue" :rows="rows" :disabled="disabled" :required="required" :aria-invalid="error ? 'true' : undefined" :aria-describedby="error ? `${textareaID}-error` : help ? `${textareaID}-help` : undefined" @input="$emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"></textarea>
    <span v-if="error" :id="`${textareaID}-error`" class="ds-field__message ds-field__message--error">{{ error }}</span>
    <span v-else-if="help" :id="`${textareaID}-help`" class="ds-field__message">{{ help }}</span>
  </label>
</template>

<style scoped>
.ds-field { display: grid; gap: var(--space-2); color: var(--text-primary); }
.ds-field__label { font-size: var(--font-size-sm); font-weight: var(--font-weight-medium); }
.ds-textarea { width: 100%; min-height: 7rem; padding: var(--space-3) var(--space-4); resize: vertical; border: 1px solid var(--border-strong); border-radius: var(--radius-control); outline: 0; background: var(--surface-solid); color: var(--text-primary); line-height: var(--line-height-body); }
.ds-textarea:focus { border-color: var(--accent); box-shadow: 0 0 0 3px var(--focus-ring); }
.ds-textarea--error { border-color: var(--danger); }
.ds-field__message { color: var(--text-tertiary); font-size: var(--font-size-xs); }
.ds-field__message--error { color: var(--danger); }
</style>
