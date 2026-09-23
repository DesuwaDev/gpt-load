<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import {
  credentialBaseUrlMaxLength,
  validCodexCustomBaseURL,
} from './credential-base-url'

const props = defineProps<{
  open: boolean
  mask: string
  value: string
  busy?: boolean
}>()

const emit = defineEmits<{
  'update:open': [open: boolean]
  apply: [payload: { base_url: string }]
}>()

const { t } = useI18n()

const draft = ref(props.value)

watch(
  () => [props.open, props.value] as const,
  ([open, value]) => {
    if (open) {
      draft.value = value
    }
  },
  { immediate: true },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCodexCustomBaseURL(trimmed.value))
const dirty = computed(() => trimmed.value !== (props.value ?? ''))
const canApply = computed(() => !props.busy && !invalid.value && dirty.value)

function setOpen(open: boolean): void {
  if (!open && props.busy) return
  emit('update:open', open)
}

function apply(): void {
  if (!canApply.value) return
  emit('apply', { base_url: trimmed.value })
}

function clear(): void {
  if (props.busy || props.value === '') return
  draft.value = ''
  emit('apply', { base_url: '' })
}
</script>

<template>
  <AppDialog
    appearance="ledger"
    :open="open"
    :title="t('group.credentials.customGateway.title')"
    :description="t('group.credentials.customGateway.hint')"
    :close-label="t('common.close')"
    :dismissible="!busy"
    @update:open="setOpen"
  >
    <template #body>
      <div class="credential-base-url-dialog">
        <p class="credential-base-url-dialog__credential">
          <span>{{ t('group.credentials.test.fields.credential') }}</span>
          <strong>{{ mask }}</strong>
        </p>

        <div class="credential-base-url-dialog__field">
          <label class="credential-base-url-dialog__label" for="credential-custom-base-url-input">
            {{ t('group.credentials.customGateway.title') }}
          </label>
          <input
            id="credential-custom-base-url-input"
            v-model="draft"
            class="credential-base-url-dialog__input"
            :class="{ 'credential-base-url-dialog__input--invalid': invalid }"
            type="url"
            spellcheck="false"
            autocomplete="off"
            :maxlength="credentialBaseUrlMaxLength"
            :disabled="busy"
            :placeholder="t('group.credentials.customGateway.placeholder')"
            :aria-invalid="invalid"
            @keydown.enter.prevent="apply"
          />
          <p class="credential-base-url-dialog__status">
            <span
              class="credential-base-url-dialog__state"
              :class="
                value === ''
                  ? 'credential-base-url-dialog__state--off'
                  : 'credential-base-url-dialog__state--on'
              "
            >
              {{
                value === ''
                  ? t('group.credentials.customGateway.default')
                  : t('group.credentials.customGateway.active')
              }}
            </span>
            <span v-if="invalid" class="credential-base-url-dialog__error" role="alert">
              {{ t('group.credentials.customGateway.invalid') }}
            </span>
          </p>
        </div>
      </div>
    </template>

    <template #footer>
      <div class="credential-base-url-dialog__footer">
        <AppButton
          v-if="value !== ''"
          type="button"
          variant="secondary"
          size="compact"
          :disabled="busy"
          @click="clear"
        >
          {{ t('group.credentials.customGateway.clear') }}
        </AppButton>
        <span class="credential-base-url-dialog__spacer"></span>
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
          {{ t('group.credentials.customGateway.apply') }}
        </AppButton>
      </div>
    </template>
  </AppDialog>
</template>

<style scoped>
.credential-base-url-dialog {
  display: grid;
  gap: var(--space-4);
}

.credential-base-url-dialog__credential {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--space-2);
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.credential-base-url-dialog__credential strong {
  min-width: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.credential-base-url-dialog__field {
  display: grid;
  gap: 6px;
}

.credential-base-url-dialog__label {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

.credential-base-url-dialog__input {
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

.credential-base-url-dialog__input:focus {
  outline: 2px solid var(--color-focus);
  outline-offset: -1px;
}

.credential-base-url-dialog__input--invalid {
  border-color: var(--color-danger);
}

.credential-base-url-dialog__status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--text-label-xs);
}

.credential-base-url-dialog__state {
  display: inline-flex;
  align-items: center;
  border-radius: var(--radius-pill);
  padding: 2px 7px;
  font-weight: 560;
}

.credential-base-url-dialog__state--on {
  background: color-mix(in srgb, var(--color-info) 14%, transparent);
  color: var(--color-info);
}

.credential-base-url-dialog__state--off {
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
}

.credential-base-url-dialog__error {
  color: var(--color-danger);
}

.credential-base-url-dialog__footer {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-2);
}

.credential-base-url-dialog__spacer {
  flex: 1;
}
</style>
