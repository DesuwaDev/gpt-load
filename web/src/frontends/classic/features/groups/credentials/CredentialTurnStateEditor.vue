<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { credentialTurnStateMaxLength, validCredentialTurnState } from './credential-turn-state'

const props = defineProps<{ value: string; disabled?: boolean }>()
const emit = defineEmits<{ apply: [payload: { codex_turn_state: string }] }>()

const { n, t } = useI18n()

const draft = ref(props.value)

// 菜单实例在行间复用，上游刷新出新值时要把草稿拉回当前值。
watch(
  () => props.value,
  (value) => {
    draft.value = value
  },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCredentialTurnState(trimmed.value))
const dirty = computed(() => trimmed.value !== props.value)
const canApply = computed(() => !props.disabled && !invalid.value && dirty.value)

function apply(): void {
  if (!canApply.value) return
  emit('apply', { codex_turn_state: trimmed.value })
}

function clear(): void {
  if (props.disabled || props.value === '') return
  draft.value = ''
  emit('apply', { codex_turn_state: '' })
}
</script>

<template>
  <div class="credential-turn-state">
    <p class="credential-turn-state__title">{{ t('group.credentials.turnState.title') }}</p>
    <p class="credential-turn-state__hint">{{ t('group.credentials.turnState.hint') }}</p>
    <textarea
      v-model="draft"
      class="credential-turn-state__input"
      :class="{ 'credential-turn-state__input--invalid': invalid }"
      rows="3"
      spellcheck="false"
      :maxlength="credentialTurnStateMaxLength"
      :disabled="disabled"
      :placeholder="t('group.credentials.turnState.placeholder')"
      :aria-label="t('group.credentials.turnState.title')"
      :aria-invalid="invalid"
    ></textarea>
    <p class="credential-turn-state__status">
      <span
        class="credential-turn-state__state"
        :class="
          value === '' ? 'credential-turn-state__state--off' : 'credential-turn-state__state--on'
        "
      >
        {{
          value === ''
            ? t('group.credentials.turnState.inactive')
            : t('group.credentials.turnState.active')
        }}
      </span>
      <span v-if="invalid" class="credential-turn-state__error">
        {{ t('group.credentials.turnState.invalid') }}
      </span>
      <span v-else class="credential-turn-state__length">
        {{ t('group.credentials.turnState.length', { count: n(trimmed.length) }) }}
      </span>
    </p>
    <div class="credential-turn-state__actions">
      <button
        class="credential-turn-state__clear"
        type="button"
        :disabled="disabled || value === ''"
        @click="clear"
      >
        {{ t('group.credentials.turnState.clear') }}
      </button>
      <button
        class="credential-turn-state__apply"
        type="button"
        :disabled="!canApply"
        @click="apply"
      >
        {{ t('group.credentials.turnState.apply') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.credential-turn-state {
  display: grid;
  gap: 5px;
  padding: 2px 6px 6px;
}
.credential-turn-state__title {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 650;
  letter-spacing: 0.02em;
}
.credential-turn-state__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
/* 实测的 state 在 300 字符上下，三行等宽足够读出开头和结尾的差异。 */
.credential-turn-state__input {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 4px 6px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: 1.5;
  resize: vertical;
  overflow-wrap: anywhere;
}
.credential-turn-state__input:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
.credential-turn-state__input--invalid {
  border-color: var(--color-danger);
}
.credential-turn-state__status {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: var(--text-label-xs);
}
.credential-turn-state__state {
  font-weight: 650;
}
.credential-turn-state__state--on {
  color: var(--color-info);
}
.credential-turn-state__state--off {
  color: var(--color-text-faint);
}
.credential-turn-state__length {
  margin-left: auto;
  color: var(--color-text-faint);
}
.credential-turn-state__error {
  margin-left: auto;
  color: var(--color-danger);
}
.credential-turn-state__actions {
  display: flex;
  gap: 4px;
}
.credential-turn-state .credential-turn-state__clear,
.credential-turn-state .credential-turn-state__apply {
  flex: 1;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 3px 8px;
  font: inherit;
  font-size: var(--text-label-xs);
  font-weight: 650;
  line-height: 1.6;
  cursor: pointer;
}
.credential-turn-state .credential-turn-state__apply {
  border-color: transparent;
  background: var(--color-info);
  color: var(--color-text-inverse);
}
.credential-turn-state .credential-turn-state__clear:disabled,
.credential-turn-state .credential-turn-state__apply:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.credential-turn-state .credential-turn-state__clear:focus-visible,
.credential-turn-state .credential-turn-state__apply:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
</style>
