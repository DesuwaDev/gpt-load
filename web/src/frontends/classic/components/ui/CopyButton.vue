<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { useClipboardCopy } from '@/app/use-clipboard-copy'
import CopyFallbackDialog from './CopyFallbackDialog.vue'

const props = withDefaults(
  defineProps<{
    value: string
    label: string
    successLabel: string
    failureLabel: string
    size?: 'md' | 'compact' | 'xs'
    variant?: 'default' | 'ghost'
    /** 上游的紧凑写法：等价于 size="compact" + variant="ghost"。 */
    compact?: boolean
  }>(),
  {
    size: 'compact',
    variant: 'default',
    compact: false,
  },
)
const resolvedSize = computed(() => (props.compact ? 'compact' : props.size))
const resolvedVariant = computed(() => (props.compact ? 'ghost' : props.variant))
const iconSize = computed(() => {
  if (resolvedSize.value === 'xs') return 13
  if (resolvedSize.value === 'compact') return 14
  return 16
})
const { copy: copyValue, fallbackText, pending, reset } = useClipboardCopy()
const state = ref<'idle' | 'success' | 'failure'>('idle')
let resetTimer: number | undefined

async function copy(): Promise<void> {
  if (pending.value) return
  state.value = 'idle'
  try {
    const result = await copyValue(props.value)
    if (result === 'cancelled') return
    state.value = result === 'success' ? 'success' : 'idle'
  } catch {
    state.value = 'failure'
  }
  window.clearTimeout(resetTimer)
  resetTimer = window.setTimeout(() => (state.value = 'idle'), 2000)
}

watch(
  () => props.value,
  () => {
    reset()
    state.value = 'idle'
  },
  { flush: 'sync' },
)

onBeforeUnmount(() => window.clearTimeout(resetTimer))
</script>

<template>
  <span
    class="copy-control"
    :class="[`copy-control--${resolvedSize}`, `copy-control--${resolvedVariant}`]"
  >
    <button
      type="button"
      :aria-label="label"
      :aria-busy="pending"
      :disabled="pending"
      @click="copy"
    >
      <Check v-if="state === 'success'" :size="iconSize" aria-hidden="true" />
      <Copy v-else :size="iconSize" aria-hidden="true" />
    </button>
    <span
      v-if="state !== 'idle'"
      class="copy-control__feedback"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ state === 'success' ? successLabel : failureLabel }}
    </span>
    <CopyFallbackDialog v-if="fallbackText !== undefined" :value="fallbackText" @close="reset" />
  </span>
</template>

<style scoped>
.copy-control {
  position: relative;
  display: inline-flex;
  flex: none;
  vertical-align: middle;
}
.copy-control button {
  display: inline-flex;
  width: 28px;
  height: 28px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control-compact, 6px);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}
.copy-control button:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
  color: var(--color-text);
}
.copy-control button:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
.copy-control--md button {
  width: var(--control-md, 36px);
  height: var(--control-md, 36px);
  border-radius: var(--radius-control);
}
.copy-control--compact button {
  width: 28px;
  height: 28px;
  border-radius: var(--radius-control-compact, 6px);
}
.copy-control--xs button {
  width: 24px;
  height: 24px;
  border-radius: var(--radius-control-compact, 6px);
}
.copy-control--ghost button {
  border-color: transparent;
  background: transparent;
  color: var(--color-text-faint);
}
.copy-control--ghost button:hover:not(:disabled) {
  border-color: transparent;
  background: var(--color-surface-sunken);
  color: var(--color-text);
}
.copy-control button:hover:not(:disabled) {
  background: var(--color-surface-sunken);
  color: var(--color-action);
}
.copy-control button:disabled {
  cursor: wait;
  opacity: 0.55;
}
.copy-control__feedback {
  position: absolute;
  z-index: var(--z-popover);
  top: calc(100% + var(--space-1));
  right: 0;
  width: max-content;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-1) var(--space-2);
  box-shadow: var(--shadow-card);
  font-size: 0.75rem;
}
</style>
