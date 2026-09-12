<script setup lang="ts">
import { PencilLine } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import IconButton from '@/components/ui/IconButton.vue'

// 凭据本地限额面板：RPM 与并发各一格，与权重面板同款 setting-panel 外壳。
// 0 表示不限，输入框留空即为 0。
const props = defineProps<{
  credentialId: number
  rpmLimit: number
  concurrencyLimit: number
  busy: boolean
  disabled: boolean
}>()
const emit = defineEmits<{
  save: [payload: { rpm_limit: number; concurrency_limit: number }]
}>()
const { n, t } = useI18n()
const editing = ref(false)
const draftRpm = ref('')
const draftConcurrency = ref('')
const rpmInputId = computed(() => `credential-rpm-${props.credentialId}`)
const concurrencyInputId = computed(() => `credential-concurrency-${props.credentialId}`)

function valid(value: string): boolean {
  if (value === '') return true
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed >= 0
}
const draftValid = computed(() => valid(draftRpm.value) && valid(draftConcurrency.value))
const summary = computed(() => {
  const rpm =
    props.rpmLimit === 0
      ? t('group.credentials.limits.unlimited')
      : t('group.credentials.limits.rpmValue', { count: n(props.rpmLimit) })
  const concurrency =
    props.concurrencyLimit === 0
      ? t('group.credentials.limits.unlimited')
      : t('group.credentials.limits.concurrencyValue', { count: n(props.concurrencyLimit) })
  return `${rpm} · ${concurrency}`
})

function resetDraft(): void {
  draftRpm.value = props.rpmLimit === 0 ? '' : String(props.rpmLimit)
  draftConcurrency.value = props.concurrencyLimit === 0 ? '' : String(props.concurrencyLimit)
}
function beginEdit(): void {
  if (props.busy || props.disabled) return
  resetDraft()
  editing.value = true
}
function save(): void {
  if (props.busy || !draftValid.value) return
  emit('save', {
    rpm_limit: draftRpm.value === '' ? 0 : Number(draftRpm.value),
    concurrency_limit: draftConcurrency.value === '' ? 0 : Number(draftConcurrency.value),
  })
  editing.value = false
}
watch(
  () => [props.rpmLimit, props.concurrencyLimit],
  () => {
    if (!editing.value) resetDraft()
  },
  { immediate: true },
)
defineExpose({ beginEdit })
</script>

<template>
  <div class="setting-panel">
    <span class="setting-panel__title">{{ t('group.credentials.limits.title') }}</span>
    <div class="setting-panel__body">
      <template v-if="!editing">
        <span class="setting-panel__value">{{ summary }}</span>
        <IconButton
          class="setting-panel__edit"
          variant="ghost"
          tone="action"
          size="xs"
          :label="t('group.credentials.limits.edit')"
          :disabled="busy || disabled"
          @click="beginEdit"
        >
          <PencilLine :size="12" aria-hidden="true" />
        </IconButton>
      </template>
      <form v-else class="setting-panel__form credential-limits__form" @submit.prevent="save">
        <label :for="rpmInputId">{{ t('group.credentials.limits.rpm') }}</label>
        <input
          :id="rpmInputId"
          v-model="draftRpm"
          type="number"
          min="0"
          step="1"
          inputmode="numeric"
          :placeholder="t('group.credentials.limits.placeholder')"
          :disabled="busy"
          :aria-invalid="!valid(draftRpm) || undefined"
        />
        <label :for="concurrencyInputId">{{ t('group.credentials.limits.concurrency') }}</label>
        <input
          :id="concurrencyInputId"
          v-model="draftConcurrency"
          type="number"
          min="0"
          step="1"
          inputmode="numeric"
          :placeholder="t('group.credentials.limits.placeholder')"
          :disabled="busy"
          :aria-invalid="!valid(draftConcurrency) || undefined"
        />
        <div class="setting-panel__actions">
          <AppButton variant="ghost" size="compact" @click="editing = false">
            {{ t('group.credentials.weightEditor.cancel') }}
          </AppButton>
          <AppButton type="submit" size="compact" :disabled="busy || !draftValid">
            {{ t('group.credentials.weightEditor.save') }}
          </AppButton>
        </div>
        <p v-if="!draftValid" class="setting-panel__error" role="alert">
          {{ t('group.credentials.limits.invalid') }}
        </p>
      </form>
    </div>
  </div>
</template>

<style scoped>
.credential-limits__form {
  display: grid;
  grid-template-columns: auto 72px;
  align-items: center;
  gap: 6px 8px;
}
.credential-limits__form > label {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.credential-limits__form > input {
  min-height: 26px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 6px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}
.credential-limits__form > .setting-panel__actions,
.credential-limits__form > .setting-panel__error {
  grid-column: 1 / -1;
}
</style>
