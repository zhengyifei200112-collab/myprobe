<script setup lang="ts">
import DsDialog from './DsDialog.vue'

withDefaults(defineProps<{ open: boolean; title: string; description?: string; closeLabel?: string; dismissible?: boolean }>(), { description: '', closeLabel: '关闭', dismissible: true })
defineEmits<{ close: [] }>()
</script>

<template><DsDialog :open="open" :title="title" :description="description" :close-label="closeLabel" :dismissible="dismissible" presentation="sheet" @close="$emit('close')"><slot></slot><template v-if="$slots.footer" #footer><slot name="footer"></slot></template></DsDialog></template>
