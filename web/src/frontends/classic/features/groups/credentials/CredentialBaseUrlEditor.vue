<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  credentialBaseUrlMaxLength,
  validCodexCustomBaseURL,
} from './credential-base-url'

const props = defineProps<{
  value: string
  disabled?: boolean
}>()
const emit = defineEmits<{
  apply: [payload: { base_url: string }]
}>()

const { t } = useI18n()

const draft = ref(props.value)

watch(
  () => props.value,
  (value) => {
    draft.value = value
  },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCodexCustomBaseURL(trimmed.value))
const dirty = computed(() => trimmed.value !== (props.value ?? ''))
const canApply = computed(() => !props.disabled && !invalid.value && dirty.value)

function apply(): void {
  if (!canApply.value) return
  emit('apply', { base_url: trimmed.value })
}

function clear(): void {
  if (props.disabled || props.value === '') return
  draft.value = ''
  emit('apply', { base_url: '' })
}
</script>

<template>
  <div class="credential-base-url">
    <p class="credential-base-url__title">{{ t('group.credentials.customGateway.title') }}</p>
    <p class="credential-base-url__hint">{{ t('group.credentials.customGateway.hint') }}</p>
    <input
      v-model="draft"
      class="credential-base-url__input"
      :class="{ 'credential-base-url__input--invalid': invalid }"
      type="url"
      spellcheck="false"
      autocomplete="off"
      :maxlength="credentialBaseUrlMaxLength"
      :disabled="disabled"
      :placeholder="t('group.credentials.customGateway.placeholder')"
      :aria-label="t('group.credentials.customGateway.title')"
      :aria-invalid="invalid"
      @keydown.enter.prevent="apply"
    />
    <p class="credential-base-url__status">
      <span
        class="credential-base-url__state"
        :class="
          value === '' ? 'credential-base-url__state--off' : 'credential-base-url__state--on'
        "
      >
        {{
          value === ''
            ? t('group.credentials.customGateway.default')
            : t('group.credentials.customGateway.active')
        }}
      </span>
      <span v-if="invalid" class="credential-base-url__error">
        {{ t('group.credentials.customGateway.invalid') }}
      </span>
    </p>
    <div class="credential-base-url__actions">
      <button
        class="credential-base-url__clear"
        type="button"
        :disabled="disabled || value === ''"
        @click="clear"
      >
        {{ t('group.credentials.customGateway.clear') }}
      </button>
      <button
        class="credential-base-url__apply"
        type="button"
        :disabled="!canApply"
        @click="apply"
      >
        {{ t('group.credentials.customGateway.apply') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.credential-base-url {
  display: grid;
  gap: 5px;
  padding: 2px 6px 6px;
}
.credential-base-url__title {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 650;
  letter-spacing: 0.02em;
}
.credential-base-url__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
.credential-base-url__input {
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
  overflow-wrap: anywhere;
}
.credential-base-url__input:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
.credential-base-url__input--invalid {
  border-color: var(--color-danger);
}
.credential-base-url__status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  margin: 0;
  font-size: var(--text-label-xs);
}
.credential-base-url__state {
  font-weight: 650;
}
.credential-base-url__state--on {
  color: var(--color-info);
}
.credential-base-url__state--off {
  color: var(--color-text-faint);
}
.credential-base-url__error {
  margin-left: auto;
  color: var(--color-danger);
}
.credential-base-url__actions {
  display: flex;
  gap: 4px;
}
.credential-base-url .credential-base-url__clear,
.credential-base-url .credential-base-url__apply {
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
.credential-base-url .credential-base-url__apply {
  border-color: transparent;
  background: var(--color-info);
  color: var(--color-text-inverse);
}
.credential-base-url .credential-base-url__clear:disabled,
.credential-base-url .credential-base-url__apply:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
</style>
