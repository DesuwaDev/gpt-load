<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  canonicalCredentialTurnStateModels,
  credentialTurnStateMaxLength,
  credentialTurnStateModelsMaxLength,
  validCredentialTurnState,
} from './credential-turn-state'

const props = defineProps<{
  value: string
  models: string
  setAtMs?: number
  disabled?: boolean
}>()
const emit = defineEmits<{
  apply: [payload: { codex_turn_state: string; codex_turn_state_models: string }]
}>()

const { n, t } = useI18n()

const draft = ref(props.value)
const modelsDraft = ref(props.models)

// 菜单实例在行间复用，上游刷新出新值时要把草稿拉回当前值。
watch(
  () => props.value,
  (value) => {
    draft.value = value
  },
)
watch(
  () => props.models,
  (value) => {
    modelsDraft.value = value
  },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCredentialTurnState(trimmed.value))
const canonicalModels = computed(() => canonicalCredentialTurnStateModels(modelsDraft.value))
const modelsInvalid = computed(() => canonicalModels.value === null)
const dirty = computed(
  () => trimmed.value !== props.value || canonicalModels.value !== props.models,
)
const canApply = computed(
  () => !props.disabled && !invalid.value && !modelsInvalid.value && dirty.value,
)

function apply(): void {
  if (!canApply.value || canonicalModels.value === null) return
  emit('apply', {
    codex_turn_state: trimmed.value,
    codex_turn_state_models: canonicalModels.value,
  })
}

// 清除只关注入值，模型名单原样留着，方便换一个 state 之后继续复用同一份范围。
function clear(): void {
  if (props.disabled || props.value === '') return
  draft.value = ''
  emit('apply', { codex_turn_state: '', codex_turn_state_models: props.models })
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
    <p class="credential-turn-state__hint">{{ t('group.credentials.turnState.modelsHint') }}</p>
    <input
      v-model="modelsDraft"
      class="credential-turn-state__input"
      :class="{ 'credential-turn-state__input--invalid': modelsInvalid }"
      type="text"
      spellcheck="false"
      autocomplete="off"
      :maxlength="credentialTurnStateModelsMaxLength"
      :disabled="disabled"
      :placeholder="t('group.credentials.turnState.modelsPlaceholder')"
      :aria-label="t('group.credentials.turnState.models')"
      :aria-invalid="modelsInvalid"
    />
    <p class="credential-turn-state__status">
      <span
        class="credential-turn-state__state"
        :class="
          canonicalModels === ''
            ? 'credential-turn-state__state--off'
            : 'credential-turn-state__state--on'
        "
      >
        {{
          canonicalModels === ''
            ? t('group.credentials.turnState.modelsAll')
            : t('group.credentials.turnState.modelsScoped')
        }}
      </span>
      <span v-if="modelsInvalid" class="credential-turn-state__error">
        {{ t('group.credentials.turnState.modelsInvalid') }}
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
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
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

/* 竖屏手机上这个菜单只有三百来像素宽，且全靠拇指操作：放宽输入区，把按钮抬到能点中的高度。 */
@media (max-width: 560px) {
  .credential-turn-state {
    gap: 7px;
  }

  .credential-turn-state__input {
    padding: 6px 8px;
  }

  .credential-turn-state__status {
    gap: 3px 8px;
  }

  /* 窄屏里状态行必然折行，这时候再把字数推到右端只会和上一行对不齐。 */
  .credential-turn-state__length,
  .credential-turn-state__error {
    margin-left: 0;
  }

  .credential-turn-state .credential-turn-state__clear,
  .credential-turn-state .credential-turn-state__apply {
    min-height: var(--touch-target);
  }
}
</style>
