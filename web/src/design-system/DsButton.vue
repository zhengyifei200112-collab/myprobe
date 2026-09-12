<script setup lang="ts">
withDefaults(defineProps<{
  type?: 'button' | 'submit' | 'reset'
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'small' | 'medium'
  loading?: boolean
  disabled?: boolean
  iconOnly?: boolean
}>(), {
  type: 'button',
  variant: 'secondary',
  size: 'medium',
  loading: false,
  disabled: false,
  iconOnly: false,
})

defineEmits<{ click: [event: MouseEvent] }>()
</script>

<template>
  <button
    :type="type"
    class="ds-button"
    :class="[`ds-button--${variant}`, `ds-button--${size}`, { 'ds-button--icon': iconOnly }]"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    @click="$emit('click', $event)"
  >
    <span v-if="loading" class="ds-button__spinner" aria-hidden="true"></span>
    <span v-if="iconOnly" class="ds-visually-hidden"><slot name="label"></slot></span>
    <slot v-else></slot>
  </button>
</template>

<style scoped>
.ds-button {
  min-height: var(--control-height);
  padding: 0 var(--space-5);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: 1px solid transparent;
  border-radius: var(--radius-button);
  font-size: var(--font-size-sm);
  font-weight: var(--font-weight-semibold);
  line-height: 1;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-standard), border-color var(--duration-fast) var(--ease-standard), color var(--duration-fast) var(--ease-standard), transform var(--duration-fast) var(--ease-standard);
}
.ds-button:active:not(:disabled) { transform: translateY(1px); }
.ds-button--small { min-height: 2.25rem; padding-inline: var(--space-4); font-size: var(--font-size-xs); }
.ds-button--icon { width: var(--control-height); padding: 0; }
.ds-button--primary { background: var(--accent); color: #fff; }
.ds-button--primary:hover:not(:disabled) { background: var(--accent-hover); }
.ds-button--primary:active:not(:disabled) { background: var(--accent-pressed); }
.ds-button--secondary { border-color: var(--border); background: var(--surface-solid); color: var(--text-primary); box-shadow: var(--shadow-xs); }
.ds-button--secondary:hover:not(:disabled), .ds-button--ghost:hover:not(:disabled) { background: var(--control-hover); }
.ds-button--ghost { background: transparent; color: var(--text-secondary); }
.ds-button--danger { background: var(--danger); color: #fff; }
.ds-button--danger:hover:not(:disabled) { filter: brightness(.96); }
.ds-button__spinner { width: 1rem; height: 1rem; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; animation: ds-spin .7s linear infinite; }
@keyframes ds-spin { to { transform: rotate(360deg); } }
</style>
