<script setup lang="ts">
import DsButton from './DsButton.vue'
import DsDialog from './DsDialog.vue'

withDefaults(defineProps<{ open: boolean; title: string; description: string; confirmLabel?: string; cancelLabel?: string; danger?: boolean; busy?: boolean }>(), { confirmLabel: '确认', cancelLabel: '取消', danger: false, busy: false })
defineEmits<{ confirm: []; cancel: [] }>()
</script>

<template><DsDialog :open="open" :title="title" :description="description" @close="$emit('cancel')"><slot></slot><template #footer><DsButton variant="ghost" :disabled="busy" @click="$emit('cancel')">{{ cancelLabel }}</DsButton><DsButton :variant="danger ? 'danger' : 'primary'" :loading="busy" @click="$emit('confirm')">{{ confirmLabel }}</DsButton></template></DsDialog></template>
