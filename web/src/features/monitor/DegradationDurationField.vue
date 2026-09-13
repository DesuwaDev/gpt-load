<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FormField from '@/components/ui/FormField.vue'

import { degradationRanges, parseIntegerField } from './degradation-form'
import { degradationIntervalChoices } from './degradation-presenter'
import { useDegradationLabels } from './use-degradation-labels'

const props = withDefaults(
  defineProps<{
    id: string
    label: string
    modelValue: string
    description?: string
    error?: string
    placeholder?: string
    /** 传入即渲染“继承全局”的快捷项，把值清空。仅监控项级字段需要。 */
    inheritLabel?: string
    disabled?: boolean
  }>(),
  {
    description: undefined,
    error: undefined,
    placeholder: undefined,
    inheritLabel: undefined,
    disabled: false,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const { durationLabel } = useDegradationLabels()

const range = degradationRanges.interval
const presets = degradationIntervalChoices
const inheriting = computed(() => props.modelValue.trim() === '')
const current = computed(() => parseIntegerField(props.modelValue))
// 只有落在合法区间里才回显人话时长，免得半截输入闪出误导性的“1 分钟”。
const hint = computed(() => {
  const seconds = current.value
  if (seconds === undefined || seconds < range.min || seconds > range.max) return ''
  return durationLabel(seconds)
})
const fieldDescription = computed(
  () => [hint.value, props.description].filter(Boolean).join(' · ') || undefined,
)
</script>

<template>
  <FormField
    :id="id"
    class="degradation-duration-field"
    :label="label"
    :description="fieldDescription"
    :error="error"
    size="compact"
  >
    <template #default="{ describedBy, invalid }">
      <input
        :id="id"
        :value="modelValue"
        inputmode="numeric"
        autocomplete="off"
        :spellcheck="false"
        :disabled="disabled"
        :placeholder="placeholder"
        :aria-describedby="describedBy"
        :aria-invalid="invalid || undefined"
        @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
      />
      <div
        class="degradation-duration-field__presets"
        role="group"
        :aria-label="t('monitor.degradation.form.presets')"
      >
        <button
          v-if="inheritLabel"
          type="button"
          class="degradation-duration-field__preset"
          :class="{ 'degradation-duration-field__preset--active': inheriting }"
          :disabled="disabled"
          :aria-pressed="inheriting"
          @click="emit('update:modelValue', '')"
        >
          {{ inheritLabel }}
        </button>
        <button
          v-for="preset in presets"
          :key="preset"
          type="button"
          class="degradation-duration-field__preset"
          :class="{ 'degradation-duration-field__preset--active': current === preset }"
          :disabled="disabled"
          :aria-pressed="current === preset"
          @click="emit('update:modelValue', String(preset))"
        >
          {{ durationLabel(preset) }}
        </button>
      </div>
    </template>
  </FormField>
</template>

<style scoped>
.degradation-duration-field :deep(input) {
  font-variant-numeric: tabular-nums;
}

.degradation-duration-field__presets {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.degradation-duration-field__preset {
  min-height: 24px;
  border: 1px solid var(--color-border-subtle);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 0 9px;
  font: inherit;
  font-size: var(--text-label-xs);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    color var(--duration-fast) var(--easing-standard);
}

.degradation-duration-field__preset:hover:not(:disabled) {
  border-color: var(--color-border-strong);
  color: var(--color-text);
}

.degradation-duration-field__preset--active {
  border-color: var(--color-action);
  background: var(--color-action-soft);
  color: var(--color-action);
}

.degradation-duration-field__preset:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

@media (max-width: 860px) {
  .degradation-duration-field__preset {
    min-height: 30px;
  }
}
</style>
