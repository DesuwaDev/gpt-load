<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@shared/http/client-context'
import {
  updateDegradationSettings,
  type DegradationCatalogDto,
  type DegradationSettingsDto,
} from '@/app/resources/degradation'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import DegradationDurationField from './DegradationDurationField.vue'
import DegradationNumberField from './DegradationNumberField.vue'
import DegradationToggleRow from './DegradationToggleRow.vue'
import {
  degradationKeywordLimit,
  degradationRanges,
  settingsDraft,
  settingsDraftErrors,
  settingsPayload,
  type DegradationSettingsDraft,
  type DegradationSettingsField,
  type NumberRange,
} from './degradation-form'

const props = defineProps<{
  open: boolean
  settings: DegradationSettingsDto
  catalog: DegradationCatalogDto
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  saved: [settings: DegradationSettingsDto]
}>()
const client = useApiClient()
const { n, t } = useI18n()
const draft = ref<DegradationSettingsDraft>(settingsDraft(props.settings))
const showErrors = ref(false)
const saving = ref(false)
const feedback = ref('')

// 每次打开都从服务端最新配置重建草稿，抽屉关掉的改动一律作废。
watch(
  () => props.open,
  (open) => {
    if (!open) return
    draft.value = settingsDraft(props.settings)
    showErrors.value = false
    feedback.value = ''
  },
  { immediate: true },
)

const sampleRange = computed<NumberRange>(() => ({ min: 1, max: props.catalog.max_samples }))
const errors = computed(() => settingsDraftErrors(draft.value, props.catalog.max_samples))
const methodHint = computed(() =>
  t('monitor.degradation.settings.methodHint', {
    method: props.catalog.method,
    recommended: n(props.catalog.recommended_samples),
    calibrated: n(props.catalog.calibrated_samples),
  }),
)

function rangeHint(range: NumberRange): string {
  return t('monitor.degradation.form.range', { min: n(range.min), max: n(range.max) })
}

function errorOf(field: DegradationSettingsField, message: string): string | undefined {
  return showErrors.value && errors.value.has(field) ? message : undefined
}

async function save(): Promise<void> {
  if (saving.value) return
  if (errors.value.size > 0) {
    showErrors.value = true
    feedback.value = t('monitor.degradation.settings.invalid')
    return
  }
  saving.value = true
  feedback.value = ''
  try {
    const saved = await updateDegradationSettings(client, settingsPayload(draft.value))
    emit('saved', saved)
    emit('update:open', false)
  } catch {
    feedback.value = t('monitor.degradation.settings.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.degradation.settings.title')"
    :description="t('monitor.degradation.settings.description')"
    :close-label="t('common.close')"
    :dismissible="!saving"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <InlineFeedback v-if="feedback" tone="danger">{{ feedback }}</InlineFeedback>
    <form class="degradation-settings" @submit.prevent="save">
      <section class="degradation-settings__section">
        <h3>{{ t('monitor.degradation.settings.sections.schedule') }}</h3>
        <DegradationToggleRow
          v-model="draft.enabled"
          :label="t('monitor.degradation.settings.enabled')"
          :description="t('monitor.degradation.settings.enabledHint')"
          :disabled="saving"
        />
        <DegradationDurationField
          id="degradation-settings-interval"
          v-model="draft.interval_seconds"
          :label="t('monitor.degradation.settings.interval')"
          :description="t('monitor.degradation.settings.intervalHint')"
          :error="errorOf('interval_seconds', rangeHint(degradationRanges.interval))"
          :disabled="saving"
        />
        <DegradationDurationField
          id="degradation-settings-cooldown"
          v-model="draft.cooldown_interval_seconds"
          :label="t('monitor.degradation.settings.cooldown')"
          :description="t('monitor.degradation.settings.cooldownHint')"
          :error="errorOf('cooldown_interval_seconds', rangeHint(degradationRanges.interval))"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.run_when_disabled"
          :label="t('monitor.degradation.settings.runWhenDisabled')"
          :description="t('monitor.degradation.settings.runWhenDisabledHint')"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.skip_quota_exhausted"
          :label="t('monitor.degradation.settings.skipQuotaExhausted')"
          :description="t('monitor.degradation.settings.skipQuotaExhaustedHint')"
          :disabled="saving"
        />
      </section>

      <section class="degradation-settings__section">
        <h3>{{ t('monitor.degradation.settings.sections.verdict') }}</h3>
        <p class="degradation-settings__note">{{ methodHint }}</p>
        <div class="degradation-settings__grid">
          <DegradationNumberField
            id="degradation-settings-samples"
            v-model="draft.sample_count"
            :label="t('monitor.degradation.settings.sampleCount')"
            :description="t('monitor.degradation.settings.sampleCountHint')"
            :error="errorOf('sample_count', rangeHint(sampleRange))"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-probability"
            v-model="draft.min_probability_percent"
            :label="t('monitor.degradation.settings.minProbability')"
            :description="t('monitor.degradation.settings.minProbabilityHint')"
            :error="
              errorOf('min_probability_percent', rangeHint(degradationRanges.probabilityPercent))
            "
            suffix="%"
            inputmode="decimal"
            :disabled="saving"
          />
        </div>
      </section>

      <section class="degradation-settings__section">
        <h3>{{ t('monitor.degradation.settings.sections.execution') }}</h3>
        <div class="degradation-settings__grid">
          <DegradationNumberField
            id="degradation-settings-concurrency"
            v-model="draft.max_concurrent_runs"
            :label="t('monitor.degradation.settings.concurrency')"
            :description="t('monitor.degradation.settings.concurrencyHint')"
            :error="errorOf('max_concurrent_runs', rangeHint(degradationRanges.concurrency))"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-timeout"
            v-model="draft.request_timeout_seconds"
            :label="t('monitor.degradation.settings.timeout')"
            :description="t('monitor.degradation.settings.timeoutHint')"
            :error="errorOf('request_timeout_seconds', rangeHint(degradationRanges.timeout))"
            :suffix="t('monitor.degradation.units.seconds')"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-retry"
            v-model="draft.retry_limit"
            :label="t('monitor.degradation.settings.retryLimit')"
            :description="t('monitor.degradation.settings.retryLimitHint')"
            :error="errorOf('retry_limit', rangeHint(degradationRanges.retry))"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-quarantine"
            v-model="draft.quarantine_error_threshold"
            :label="t('monitor.degradation.settings.quarantine')"
            :description="t('monitor.degradation.settings.quarantineHint')"
            :error="errorOf('quarantine_error_threshold', rangeHint(degradationRanges.quarantine))"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-retention"
            v-model="draft.history_retention_days"
            class="degradation-settings__wide"
            :label="t('monitor.degradation.settings.retention')"
            :description="t('monitor.degradation.settings.retentionHint')"
            :error="errorOf('history_retention_days', rangeHint(degradationRanges.retention))"
            :suffix="t('monitor.degradation.units.days')"
            :disabled="saving"
          />
        </div>
      </section>

      <section class="degradation-settings__section">
        <h3>{{ t('monitor.degradation.settings.sections.overload') }}</h3>
        <DegradationToggleRow
          v-model="draft.overload_trigger_enabled"
          :label="t('monitor.degradation.settings.overloadTrigger')"
          :description="t('monitor.degradation.settings.overloadTriggerHint')"
          :disabled="saving"
        />
        <FormField
          id="degradation-settings-keywords"
          :label="t('monitor.degradation.settings.overloadKeywords')"
          :description="
            t('monitor.degradation.settings.overloadKeywordsHint', {
              max: n(degradationKeywordLimit),
            })
          "
          :error="
            errorOf('overload_keywords', t('monitor.degradation.settings.overloadKeywordsInvalid'))
          "
          size="compact"
        >
          <template #default="{ describedBy, invalid }">
            <input
              id="degradation-settings-keywords"
              v-model="draft.overload_keywords"
              class="degradation-settings__mono"
              autocomplete="off"
              :spellcheck="false"
              :disabled="saving"
              placeholder="overloaded, upstream_sse_error"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
            />
          </template>
        </FormField>
        <div class="degradation-settings__grid">
          <DegradationNumberField
            id="degradation-settings-overload-retry"
            v-model="draft.overload_retry_limit"
            :label="t('monitor.degradation.settings.overloadRetry')"
            :description="t('monitor.degradation.settings.overloadRetryHint')"
            :error="errorOf('overload_retry_limit', rangeHint(degradationRanges.retry))"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-overload-scan"
            v-model="draft.overload_scan_interval_seconds"
            :label="t('monitor.degradation.settings.overloadScan')"
            :description="t('monitor.degradation.settings.overloadScanHint')"
            :error="
              errorOf('overload_scan_interval_seconds', rangeHint(degradationRanges.overloadScan))
            "
            :suffix="t('monitor.degradation.units.seconds')"
            :disabled="saving"
          />
          <DegradationNumberField
            id="degradation-settings-overload-debounce"
            v-model="draft.overload_debounce_seconds"
            class="degradation-settings__wide"
            :label="t('monitor.degradation.settings.overloadDebounce')"
            :description="t('monitor.degradation.settings.overloadDebounceHint')"
            :error="
              errorOf('overload_debounce_seconds', rangeHint(degradationRanges.overloadDebounce))
            "
            :suffix="t('monitor.degradation.units.seconds')"
            :disabled="saving"
          />
        </div>
      </section>

      <section class="degradation-settings__section">
        <h3>{{ t('monitor.degradation.settings.sections.notifications') }}</h3>
        <InlineFeedback tone="info" appearance="hint">
          {{ t('monitor.degradation.settings.notifyReserved') }}
        </InlineFeedback>
        <DegradationToggleRow
          v-model="draft.notify_telegram_enabled"
          :label="t('monitor.degradation.settings.notifyTelegram')"
          :description="t('monitor.degradation.settings.notifyTelegramHint')"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.notify_email_enabled"
          :label="t('monitor.degradation.settings.notifyEmail')"
          :description="t('monitor.degradation.settings.notifyEmailHint')"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.notify_on_degraded"
          :label="t('monitor.degradation.settings.notifyOnDegraded')"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.notify_on_recovered"
          :label="t('monitor.degradation.settings.notifyOnRecovered')"
          :disabled="saving"
        />
        <DegradationToggleRow
          v-model="draft.notify_on_error"
          :label="t('monitor.degradation.settings.notifyOnError')"
          :disabled="saving"
        />
      </section>
    </form>

    <template #footer>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="saving"
        @click="emit('update:open', false)"
      >
        {{ t('common.cancel') }}
      </AppButton>
      <AppButton size="compact" :busy="saving" @click="save">
        {{ t('monitor.degradation.settings.save') }}
      </AppButton>
    </template>
  </AppDrawer>
</template>

<style scoped>
.degradation-settings,
.degradation-settings__section {
  display: grid;
  min-width: 0;
}

.degradation-settings {
  gap: 0;
}

.degradation-settings__section {
  gap: 14px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 16px 0;
}

.degradation-settings__section:last-child {
  border-bottom: 0;
}

.degradation-settings__section h3 {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 650;
}

.degradation-settings__note {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.6;
}

.degradation-settings__grid {
  display: grid;
  align-items: start;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 10px;
}

.degradation-settings__wide {
  grid-column: 1 / -1;
}

.degradation-settings__mono {
  font-family: var(--font-mono);
}

@media (max-width: 520px) {
  .degradation-settings__grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
