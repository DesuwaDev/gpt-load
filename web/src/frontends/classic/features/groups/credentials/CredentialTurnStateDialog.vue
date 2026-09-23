<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import {
  canonicalCredentialTurnStateModels,
  credentialTurnStateMaxLength,
  credentialTurnStateModelsMaxLength,
  validCredentialTurnState,
} from './credential-turn-state'

const props = defineProps<{
  open: boolean
  mask: string
  value: string
  models: string
  setAtMs?: number
  busy?: boolean
}>()

const emit = defineEmits<{
  'update:open': [open: boolean]
  apply: [payload: { codex_turn_state: string; codex_turn_state_models: string }]
}>()

const { n, t } = useI18n()

const draft = ref(props.value)
const modelsDraft = ref(props.models)

watch(
  () => [props.open, props.value, props.models] as const,
  ([open, value, models]) => {
    if (open) {
      draft.value = value
      modelsDraft.value = models
    }
  },
  { immediate: true },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCredentialTurnState(trimmed.value))
const canonicalModels = computed(() => canonicalCredentialTurnStateModels(modelsDraft.value))
const modelsInvalid = computed(() => canonicalModels.value === null)
const dirty = computed(
  () => trimmed.value !== props.value || canonicalModels.value !== props.models,
)
const canApply = computed(
  () => !props.busy && !invalid.value && !modelsInvalid.value && dirty.value,
)

function setOpen(open: boolean): void {
  if (!open && props.busy) return
  emit('update:open', open)
}

function apply(): void {
  if (!canApply.value || canonicalModels.value === null) return
  emit('apply', {
    codex_turn_state: trimmed.value,
    codex_turn_state_models: canonicalModels.value,
  })
}

function clear(): void {
  if (props.busy || props.value === '') return
  draft.value = ''
  emit('apply', { codex_turn_state: '', codex_turn_state_models: props.models })
}
</script>

<template>
  <AppDialog
    appearance="ledger"
    :open="open"
    :title="t('group.credentials.turnState.title')"
    :description="t('group.credentials.turnState.hint')"
    :close-label="t('common.close')"
    :dismissible="!busy"
    @update:open="setOpen"
  >
    <template #body>
      <div class="credential-turn-state-dialog">
        <p class="credential-turn-state-dialog__credential">
          <span>{{ t('group.credentials.test.fields.credential') }}</span>
          <strong>{{ mask }}</strong>
        </p>

        <div class="credential-turn-state-dialog__field">
          <label class="credential-turn-state-dialog__label" for="credential-turn-state-input">
            {{ t('group.credentials.turnState.title') }}
          </label>
          <textarea
            id="credential-turn-state-input"
            v-model="draft"
            class="credential-turn-state-dialog__textarea"
            :class="{ 'credential-turn-state-dialog__textarea--invalid': invalid }"
            rows="4"
            spellcheck="false"
            :maxlength="credentialTurnStateMaxLength"
            :disabled="busy"
            :placeholder="t('group.credentials.turnState.placeholder')"
            :aria-invalid="invalid"
          ></textarea>
          <p class="credential-turn-state-dialog__status">
            <span
              class="credential-turn-state-dialog__state"
              :class="
                value === ''
                  ? 'credential-turn-state-dialog__state--off'
                  : 'credential-turn-state-dialog__state--on'
              "
            >
              {{
                value === ''
                  ? t('group.credentials.turnState.inactive')
                  : t('group.credentials.turnState.active')
              }}
            </span>
            <span v-if="invalid" class="credential-turn-state-dialog__error" role="alert">
              {{ t('group.credentials.turnState.invalid') }}
            </span>
            <span v-else class="credential-turn-state-dialog__length">
              {{ t('group.credentials.turnState.length', { count: n([...trimmed].length) }) }}
            </span>
          </p>
        </div>

        <div class="credential-turn-state-dialog__field">
          <label class="credential-turn-state-dialog__label" for="credential-turn-state-models-input">
            {{ t('group.credentials.turnState.models') }}
          </label>
          <p class="credential-turn-state-dialog__hint">
            {{ t('group.credentials.turnState.modelsHint') }}
          </p>
          <input
            id="credential-turn-state-models-input"
            v-model="modelsDraft"
            class="credential-turn-state-dialog__input"
            :class="{ 'credential-turn-state-dialog__input--invalid': modelsInvalid }"
            type="text"
            spellcheck="false"
            autocomplete="off"
            :maxlength="credentialTurnStateModelsMaxLength"
            :disabled="busy"
            :placeholder="t('group.credentials.turnState.modelsPlaceholder')"
            :aria-invalid="modelsInvalid"
            @keydown.enter.prevent="apply"
          />
          <p class="credential-turn-state-dialog__status">
            <span
              class="credential-turn-state-dialog__state"
              :class="
                canonicalModels === ''
                  ? 'credential-turn-state-dialog__state--off'
                  : 'credential-turn-state-dialog__state--on'
              "
            >
              {{
                canonicalModels === ''
                  ? t('group.credentials.turnState.modelsAll')
                  : t('group.credentials.turnState.modelsScoped')
              }}
            </span>
            <span v-if="modelsInvalid" class="credential-turn-state-dialog__error" role="alert">
              {{ t('group.credentials.turnState.modelsInvalid') }}
            </span>
          </p>
        </div>
      </div>
    </template>

    <template #footer>
      <div class="credential-turn-state-dialog__footer">
        <AppButton
          v-if="value !== ''"
          type="button"
          variant="secondary"
          size="compact"
          :disabled="busy"
          @click="clear"
        >
          {{ t('group.credentials.turnState.clear') }}
        </AppButton>
        <span class="credential-turn-state-dialog__spacer"></span>
        <AppButton
          type="button"
          variant="secondary"
          size="compact"
          :disabled="busy"
          @click="setOpen(false)"
        >
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          type="button"
          tone="action"
          size="compact"
          :disabled="!canApply"
          :busy="busy"
          @click="apply"
        >
          {{ t('group.credentials.turnState.apply') }}
        </AppButton>
      </div>
    </template>
  </AppDialog>
</template>

<style scoped>
.credential-turn-state-dialog {
  display: grid;
  gap: var(--space-4);
}

.credential-turn-state-dialog__credential {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--space-2);
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.credential-turn-state-dialog__credential strong {
  min-width: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.credential-turn-state-dialog__field {
  display: grid;
  gap: 6px;
}

.credential-turn-state-dialog__label {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

.credential-turn-state-dialog__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

.credential-turn-state-dialog__textarea {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  min-height: 80px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

.credential-turn-state-dialog__textarea:focus,
.credential-turn-state-dialog__input:focus {
  outline: 2px solid var(--color-focus);
  outline-offset: -1px;
}

.credential-turn-state-dialog__textarea--invalid,
.credential-turn-state-dialog__input--invalid {
  border-color: var(--color-danger);
}

.credential-turn-state-dialog__input {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.credential-turn-state-dialog__status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--text-label-xs);
}

.credential-turn-state-dialog__state {
  display: inline-flex;
  align-items: center;
  border-radius: var(--radius-pill);
  padding: 2px 7px;
  font-weight: 560;
}

.credential-turn-state-dialog__state--on {
  background: color-mix(in srgb, var(--color-info) 14%, transparent);
  color: var(--color-info);
}

.credential-turn-state-dialog__state--off {
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
}

.credential-turn-state-dialog__length {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
}

.credential-turn-state-dialog__error {
  color: var(--color-danger);
}

.credential-turn-state-dialog__footer {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-2);
}

.credential-turn-state-dialog__spacer {
  flex: 1;
}
</style>
