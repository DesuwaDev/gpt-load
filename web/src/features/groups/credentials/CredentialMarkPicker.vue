<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialMark } from '@/api/control/types'

import {
  credentialMarkNoteMaxLength,
  credentialMarkOptionKey,
  credentialMarkOptions,
  credentialMarkTone,
} from './credential-mark'

const props = defineProps<{ mark: CredentialMark; note: string; disabled?: boolean }>()
const emit = defineEmits<{ apply: [payload: { mark: CredentialMark; mark_note: string }] }>()

const { t } = useI18n()

const draft = ref(props.note)
const editingCustom = ref(props.mark === 'custom')
const noteInput = ref<HTMLInputElement | null>(null)

// 菜单会复用同一个实例，上游刷新出新标记时要把草稿拉回当前值。
watch(
  () => [props.mark, props.note] as const,
  ([mark, note]) => {
    editingCustom.value = mark === 'custom'
    draft.value = note
  },
)

const trimmedNote = computed(() => draft.value.trim())
const canApplyCustom = computed(
  () => trimmedNote.value !== '' && [...trimmedNote.value].length <= credentialMarkNoteMaxLength,
)

function choose(option: CredentialMark): void {
  if (props.disabled) return
  if (option === 'custom') {
    // 自定义要先写标签，所以只展开输入框，不立即提交。
    editingCustom.value = true
    void nextTick(() => noteInput.value?.focus())
    return
  }
  editingCustom.value = false
  draft.value = ''
  if (option !== props.mark) emit('apply', { mark: option, mark_note: '' })
}

function applyCustom(): void {
  if (props.disabled || !canApplyCustom.value) return
  emit('apply', { mark: 'custom', mark_note: trimmedNote.value })
}
</script>

<template>
  <div class="credential-mark-picker">
    <p class="credential-mark-picker__title">{{ t('group.credentials.mark.title') }}</p>
    <div
      class="credential-mark-picker__options"
      role="group"
      :aria-label="t('group.credentials.mark.title')"
    >
      <button
        v-for="option in credentialMarkOptions"
        :key="option || 'none'"
        class="credential-mark-picker__option"
        :class="[
          `credential-mark-picker__option--${credentialMarkTone(option) ?? 'neutral'}`,
          { 'credential-mark-picker__option--active': option === mark },
        ]"
        type="button"
        :disabled="disabled"
        :aria-pressed="option === mark"
        @click="choose(option)"
      >
        {{ t(credentialMarkOptionKey(option)) }}
      </button>
    </div>
    <div v-if="editingCustom" class="credential-mark-picker__custom">
      <input
        ref="noteInput"
        v-model="draft"
        class="credential-mark-picker__input"
        type="text"
        :maxlength="credentialMarkNoteMaxLength"
        :disabled="disabled"
        :placeholder="t('group.credentials.mark.notePlaceholder')"
        :aria-label="t('group.credentials.mark.notePlaceholder')"
        @keydown.enter.prevent="applyCustom"
      />
      <button
        class="credential-mark-picker__apply"
        type="button"
        :disabled="disabled || !canApplyCustom"
        @click="applyCustom"
      >
        {{ t('group.credentials.mark.apply') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.credential-mark-picker {
  display: grid;
  gap: 5px;
  padding: 2px 6px 6px;
}
.credential-mark-picker__title {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 650;
  letter-spacing: 0.02em;
}
/* 固定两列，四个选项永远排成 2×2，不会随菜单宽度抖动。 */
.credential-mark-picker__options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px;
}
.credential-mark-picker .credential-mark-picker__option {
  display: block;
  width: 100%;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 3px 6px;
  font: inherit;
  font-size: var(--text-label-xs);
  font-weight: 650;
  line-height: 1.6;
  text-align: center;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}
.credential-mark-picker .credential-mark-picker__option:hover:not(:disabled) {
  background: var(--color-surface-sunken);
}
.credential-mark-picker .credential-mark-picker__option:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.credential-mark-picker .credential-mark-picker__option:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
/* 选中态直接用徽章的实心色，菜单里的选择和卡片上的标记对得上。 */
.credential-mark-picker .credential-mark-picker__option--active {
  border-color: transparent;
  color: var(--color-text-inverse);
}
.credential-mark-picker
  .credential-mark-picker__option--neutral.credential-mark-picker__option--active {
  border-color: var(--color-text-muted);
  background: var(--color-surface-sunken);
  color: var(--color-text);
}
.credential-mark-picker
  .credential-mark-picker__option--warning.credential-mark-picker__option--active {
  background: var(--color-warning);
}
.credential-mark-picker
  .credential-mark-picker__option--danger.credential-mark-picker__option--active {
  background: var(--color-danger);
}
.credential-mark-picker
  .credential-mark-picker__option--info.credential-mark-picker__option--active {
  background: var(--color-info);
}
.credential-mark-picker__custom {
  display: flex;
  align-items: center;
  gap: 4px;
}
.credential-mark-picker__input {
  min-width: 0;
  flex: 1;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 3px 6px;
  font: inherit;
  font-size: var(--text-label-xs);
  line-height: 1.6;
}
.credential-mark-picker__input:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
.credential-mark-picker .credential-mark-picker__apply {
  flex: none;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  background: var(--color-info);
  color: var(--color-text-inverse);
  padding: 3px 8px;
  font: inherit;
  font-size: var(--text-label-xs);
  font-weight: 650;
  line-height: 1.6;
  cursor: pointer;
}
.credential-mark-picker .credential-mark-picker__apply:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.credential-mark-picker .credential-mark-picker__apply:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
</style>
