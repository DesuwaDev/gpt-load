<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { DegradationCatalogDto, DegradationSettingsDto } from '@/app/resources/degradation'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'

import DegradationDurationField from './DegradationDurationField.vue'
import DegradationNumberField from './DegradationNumberField.vue'
import {
  degradationNoteLimit,
  degradationRanges,
  type DegradationProbeDraft,
  type DegradationProbeField,
  type NumberRange,
} from './degradation-form'
import { useDegradationLabels } from './use-degradation-labels'

const props = withDefaults(
  defineProps<{
    idPrefix: string
    draft: DegradationProbeDraft
    errors: Set<DegradationProbeField>
    showErrors: boolean
    catalog: DegradationCatalogDto
    settings: DegradationSettingsDto
    modelSuggestions?: string[]
    disabled?: boolean
  }>(),
  { modelSuggestions: () => [], disabled: false },
)
const emit = defineEmits<{ update: [field: DegradationProbeField, value: string] }>()
const { n, t } = useI18n()
const { durationLabel, effortLabel, probabilityLabel } = useDegradationLabels()

const maxSamples = computed(() => props.catalog.max_samples)
const modelOptions = computed(() => [
  { value: '', label: t('monitor.degradation.form.expectedModelPlaceholder') },
  ...props.catalog.models.map(({ id, display_name }) => ({
    value: id,
    label: display_name && display_name !== id ? `${display_name} · ${id}` : id,
  })),
])
const effortOptions = computed(() =>
  props.catalog.efforts.map((effort) => ({ value: effort, label: effortLabel(effort) })),
)
const runWhenDisabledOptions = computed(() => [
  {
    value: 'inherit',
    label: t('monitor.degradation.form.inheritOption', {
      value: props.settings.run_when_disabled
        ? t('monitor.degradation.form.on')
        : t('monitor.degradation.form.off'),
    }),
  },
  { value: 'on', label: t('monitor.degradation.form.on') },
  { value: 'off', label: t('monitor.degradation.form.off') },
])
const suggestionsId = computed(() => `${props.idPrefix}-model-suggestions`)

function rangeHint(range: NumberRange): string {
  return t('monitor.degradation.form.range', { min: n(range.min), max: n(range.max) })
}

function inheritHint(value: string): string {
  return t('monitor.degradation.form.inheritHint', { value })
}

function errorOf(field: DegradationProbeField, message: string): string | undefined {
  return props.showErrors && props.errors.has(field) ? message : undefined
}

function update(field: DegradationProbeField, value: string): void {
  emit('update', field, value)
}
</script>

<template>
  <div class="degradation-probe-fields">
    <div class="degradation-probe-fields__grid">
      <FormField
        :id="`${idPrefix}-upstream-model`"
        class="degradation-probe-fields__wide"
        :label="t('monitor.degradation.form.upstreamModel')"
        :description="t('monitor.degradation.form.upstreamModelHint')"
        :error="
          errorOf('upstream_model', t('monitor.degradation.form.upstreamModelRequired'))
        "
        required
        :required-text="t('monitor.degradation.form.required')"
        size="compact"
      >
        <template #default="{ describedBy, invalid }">
          <input
            :id="`${idPrefix}-upstream-model`"
            :value="draft.upstream_model"
            class="degradation-probe-fields__mono"
            :list="modelSuggestions.length ? suggestionsId : undefined"
            autocomplete="off"
            :spellcheck="false"
            :disabled="disabled"
            :placeholder="t('monitor.degradation.form.upstreamModelPlaceholder')"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            @input="update('upstream_model', ($event.target as HTMLInputElement).value)"
          />
          <datalist v-if="modelSuggestions.length" :id="suggestionsId">
            <option v-for="model in modelSuggestions" :key="model" :value="model" />
          </datalist>
        </template>
      </FormField>

      <FormField
        :id="`${idPrefix}-expected-model`"
        class="degradation-probe-fields__wide"
        :label="t('monitor.degradation.form.expectedModel')"
        :description="t('monitor.degradation.form.expectedModelHint')"
        :error="
          errorOf('expected_model', t('monitor.degradation.form.expectedModelRequired'))
        "
        required
        :required-text="t('monitor.degradation.form.required')"
        size="compact"
      >
        <AppSelect
          :id="`${idPrefix}-expected-model`"
          :model-value="draft.expected_model"
          :label="t('monitor.degradation.form.expectedModel')"
          :options="modelOptions"
          :disabled="disabled"
          size="sm"
          @update:model-value="update('expected_model', $event)"
        />
      </FormField>

      <FormField
        :id="`${idPrefix}-effort`"
        :label="t('monitor.degradation.form.reasoningEffort')"
        :description="t('monitor.degradation.form.reasoningEffortHint')"
        size="compact"
      >
        <AppSelect
          :id="`${idPrefix}-effort`"
          :model-value="draft.reasoning_effort"
          :label="t('monitor.degradation.form.reasoningEffort')"
          :options="effortOptions"
          :disabled="disabled"
          size="sm"
          @update:model-value="update('reasoning_effort', $event)"
        />
      </FormField>

      <FormField
        :id="`${idPrefix}-run-when-disabled`"
        :label="t('monitor.degradation.form.runWhenDisabled')"
        :description="t('monitor.degradation.form.runWhenDisabledHint')"
        size="compact"
      >
        <AppSelect
          :id="`${idPrefix}-run-when-disabled`"
          :model-value="draft.run_when_disabled"
          :label="t('monitor.degradation.form.runWhenDisabled')"
          :options="runWhenDisabledOptions"
          :disabled="disabled"
          size="sm"
          @update:model-value="update('run_when_disabled', $event)"
        />
      </FormField>

      <DegradationNumberField
        :id="`${idPrefix}-sample-count`"
        :label="t('monitor.degradation.form.sampleCount')"
        :model-value="draft.sample_count"
        :description="inheritHint(n(settings.sample_count))"
        :error="
          errorOf('sample_count', rangeHint({ min: 1, max: maxSamples }))
        "
        :placeholder="t('monitor.degradation.form.inheritPlaceholder')"
        :disabled="disabled"
        @update:model-value="update('sample_count', $event)"
      />

      <DegradationNumberField
        :id="`${idPrefix}-min-probability`"
        :label="t('monitor.degradation.form.minProbability')"
        :model-value="draft.min_probability_percent"
        :description="inheritHint(probabilityLabel(settings.min_probability_micros))"
        :error="
          errorOf('min_probability_percent', rangeHint(degradationRanges.probabilityPercent))
        "
        :placeholder="t('monitor.degradation.form.inheritPlaceholder')"
        suffix="%"
        inputmode="decimal"
        :disabled="disabled"
        @update:model-value="update('min_probability_percent', $event)"
      />

      <DegradationDurationField
        :id="`${idPrefix}-interval`"
        class="degradation-probe-fields__wide"
        :label="t('monitor.degradation.form.interval')"
        :model-value="draft.interval_seconds"
        :description="inheritHint(durationLabel(settings.interval_seconds))"
        :error="errorOf('interval_seconds', rangeHint(degradationRanges.interval))"
        :placeholder="t('monitor.degradation.form.inheritPlaceholder')"
        :inherit-label="t('monitor.degradation.form.inherit')"
        :disabled="disabled"
        @update:model-value="update('interval_seconds', $event)"
      />

      <DegradationDurationField
        :id="`${idPrefix}-cooldown`"
        class="degradation-probe-fields__wide"
        :label="t('monitor.degradation.form.cooldownInterval')"
        :model-value="draft.cooldown_interval_seconds"
        :description="inheritHint(durationLabel(settings.cooldown_interval_seconds))"
        :error="errorOf('cooldown_interval_seconds', rangeHint(degradationRanges.interval))"
        :placeholder="t('monitor.degradation.form.inheritPlaceholder')"
        :inherit-label="t('monitor.degradation.form.inherit')"
        :disabled="disabled"
        @update:model-value="update('cooldown_interval_seconds', $event)"
      />

      <FormField
        :id="`${idPrefix}-note`"
        class="degradation-probe-fields__wide"
        :label="t('monitor.degradation.form.note')"
        :label-suffix="t('monitor.degradation.form.optional')"
        :description="t('monitor.degradation.form.noteHint')"
        :error="
          errorOf('note', t('monitor.degradation.form.noteTooLong', { max: n(degradationNoteLimit) }))
        "
        size="compact"
      >
        <template #default="{ describedBy, invalid }">
          <input
            :id="`${idPrefix}-note`"
            :value="draft.note"
            :maxlength="degradationNoteLimit"
            autocomplete="off"
            :disabled="disabled"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            @input="update('note', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
    </div>
  </div>
</template>

<style scoped>
.degradation-probe-fields__grid {
  display: grid;
  align-items: start;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 10px;
}

.degradation-probe-fields__wide {
  grid-column: 1 / -1;
}

.degradation-probe-fields__grid :deep(.app-select__trigger) {
  width: 100%;
}

.degradation-probe-fields__mono {
  font-family: var(--font-mono);
}

@media (max-width: 520px) {
  .degradation-probe-fields__grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
