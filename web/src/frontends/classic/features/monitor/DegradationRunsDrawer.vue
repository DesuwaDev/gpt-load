<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@shared/http/client-context'
import {
  degradationRunsQueryOptions,
  probabilityMicrosScale,
  type DegradationMonitorDto,
  type DegradationRunDto,
} from '@/app/resources/degradation'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { degradationStateTone } from './degradation-presenter'
import { useDegradationLabels } from './use-degradation-labels'

const props = defineProps<{ open: boolean; monitor?: DegradationMonitorDto }>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const { locale, n, t } = useI18n()
const { probabilityLabel, reasonLabels, stateLabel, tokenLabel, triggerLabel } =
  useDegradationLabels()

const monitorID = computed(() => (props.open ? props.monitor?.id : undefined))
const runsQuery = useQuery(degradationRunsQueryOptions(client, monitorID))
const runs = computed<DegradationRunDto[]>(() => runsQuery.data.value?.items ?? [])
const selectedID = ref<number>()

// 历史列表是时间倒序的，换监控项或刷新后默认落在最近一次检测上。
watch(
  runs,
  (items) => {
    if (items.length === 0) {
      selectedID.value = undefined
      return
    }
    if (selectedID.value === undefined || !items.some((run) => run.id === selectedID.value)) {
      selectedID.value = items[0].id
    }
  },
  { immediate: true },
)

const selected = computed(() => runs.value.find((run) => run.id === selectedID.value))
const targetLabel = computed(() => {
  const monitor = props.monitor
  if (!monitor) return ''
  return monitor.credential_id > 0
    ? `${monitor.group_name} · ${monitor.credential_mask}`
    : monitor.group_name
})
const ranking = computed(() => {
  const entries = selected.value?.ranking ?? []
  const peak = entries.reduce((max, entry) => Math.max(max, entry.probability_micros), 0)
  return entries.map((entry) => ({
    ...entry,
    share: peak > 0 ? Math.round((entry.probability_micros / peak) * 100) : 0,
  }))
})

function durationText(ms: number): string {
  return ms >= 1_000
    ? t('monitor.degradation.runs.durationSeconds', { value: n(Math.round(ms / 100) / 10) })
    : t('monitor.degradation.runs.durationMs', { value: n(ms) })
}

function modelText(run: DegradationRunDto): string {
  if (run.detected_model === '') return t('monitor.degradation.runs.noDetection')
  return run.detected_model_name && run.detected_model_name !== run.detected_model
    ? `${run.detected_model_name} · ${run.detected_model}`
    : run.detected_model
}

function isThresholdMet(run: DegradationRunDto): boolean {
  return run.expected_probability_micros >= run.min_probability_micros
}

function barWidth(micros: number): string {
  return `${Math.min(100, Math.max(0, (micros / probabilityMicrosScale) * 100))}%`
}

// 统计来自后端，覆盖这条监控保留的全部历史，不随抽屉里显示的条数变化。
const stats = computed(() => runsQuery.data.value?.stats)
const outcomeStats = computed(() => {
  const value = stats.value
  if (!value) return []
  const entries = [
    { key: 'healthy', label: t('monitor.degradation.runs.statsHealthy'), count: value.healthy },
    { key: 'degraded', label: t('monitor.degradation.runs.statsDegraded'), count: value.degraded },
    {
      key: 'inconclusive',
      label: t('monitor.degradation.runs.statsInconclusive'),
      count: value.inconclusive,
    },
    { key: 'error', label: t('monitor.degradation.runs.statsError'), count: value.error },
    {
      key: 'quota_exhausted',
      label: t('monitor.degradation.runs.statsQuota'),
      count: value.quota_exhausted,
    },
    { key: 'unknown', label: t('monitor.degradation.runs.statsUnknown'), count: value.unknown },
  ]
  // unknown 只在真的出现过时才占一格，正常情况下它恒为 0。
  return entries.filter((entry) => entry.key !== 'unknown' || entry.count > 0)
})
const triggerStats = computed(() => {
  const value = stats.value
  if (!value) return []
  return [
    { key: 'schedule', label: t('monitor.degradation.runs.statsSchedule'), count: value.schedule },
    { key: 'manual', label: t('monitor.degradation.runs.statsManual'), count: value.manual },
    { key: 'overload', label: t('monitor.degradation.runs.statsOverload'), count: value.overload },
  ]
})

const samples = computed(() => selected.value?.samples ?? [])
// 复制全部时给每段加一个序号分隔，贴到别处仍能看出是第几次采样。
const allSamplesText = computed(() =>
  samples.value.map((sample, position) => `# ${position + 1}\n${sample.text}`).join('\n\n'),
)
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.degradation.runs.title')"
    :description="targetLabel || t('monitor.degradation.runs.description')"
    :close-label="t('common.close')"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <QueryFeedback
      v-if="runsQuery.isPending.value"
      state="loading"
      :message="t('monitor.degradation.runs.loading')"
    />
    <QueryFeedback
      v-else-if="runsQuery.isError.value"
      state="error"
      :message="t('monitor.degradation.runs.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="runsQuery.refetch()"
    />
    <EmptyState
      v-else-if="runs.length === 0"
      variant="ledger"
      :title="t('monitor.degradation.runs.emptyTitle')"
      :description="t('monitor.degradation.runs.emptyDescription')"
    />

    <div v-else class="degradation-runs">
      <section v-if="stats" class="degradation-runs__stats">
        <h3>
          {{ t('monitor.degradation.runs.stats') }}
          <span>{{ t('monitor.degradation.runs.statsTotal', { count: n(stats.total) }) }}</span>
        </h3>
        <dl class="degradation-runs__stats-row">
          <div v-for="entry in outcomeStats" :key="entry.key">
            <dt>{{ entry.label }}</dt>
            <dd :class="`degradation-runs__stat--${entry.key}`">{{ n(entry.count) }}</dd>
          </div>
        </dl>
        <dl class="degradation-runs__stats-row degradation-runs__stats-row--trigger">
          <div v-for="entry in triggerStats" :key="entry.key">
            <dt>{{ entry.label }}</dt>
            <dd>{{ n(entry.count) }}</dd>
          </div>
        </dl>
        <p class="degradation-runs__stats-hint">{{ t('monitor.degradation.runs.statsHint') }}</p>
      </section>

      <ul class="degradation-runs__list" :aria-label="t('monitor.degradation.runs.listLabel')">
        <li v-for="run in runs" :key="run.id">
          <button
            type="button"
            class="degradation-runs__entry"
            :class="{ 'degradation-runs__entry--active': run.id === selectedID }"
            :aria-current="run.id === selectedID"
            @click="selectedID = run.id"
          >
            <StatusBadge :tone="degradationStateTone(run.outcome)" size="compact">
              {{ stateLabel(run.outcome) }}
            </StatusBadge>
            <span class="degradation-runs__entry-meta">
              <AppRelativeTime
                :instant="run.started_at_ms || null"
                :locale="locale"
                empty-label="—"
              />
              <small>{{ triggerLabel(run.trigger) }} · {{ durationText(run.duration_ms) }}</small>
            </span>
            <span class="degradation-runs__entry-probability">
              {{ probabilityLabel(run.expected_probability_micros) }}
            </span>
          </button>
        </li>
      </ul>

      <section v-if="selected" class="degradation-runs__detail">
        <h3>{{ t('monitor.degradation.runs.detailTitle') }}</h3>
        <dl class="degradation-runs__facts">
          <div>
            <dt>{{ t('monitor.degradation.runs.expected') }}</dt>
            <dd class="degradation-runs__mono">{{ selected.expected_model }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.detected') }}</dt>
            <dd class="degradation-runs__mono">{{ modelText(selected) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.expectedProbability') }}</dt>
            <dd
              :class="
                isThresholdMet(selected)
                  ? 'degradation-runs__value--success'
                  : 'degradation-runs__value--danger'
              "
            >
              {{ probabilityLabel(selected.expected_probability_micros) }}
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.threshold') }}</dt>
            <dd>{{ probabilityLabel(selected.min_probability_micros) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.leadingProbability') }}</dt>
            <dd>{{ probabilityLabel(selected.leading_probability_micros) }}</dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.samples') }}</dt>
            <dd>
              {{
                t('monitor.degradation.runs.samplesValue', {
                  used: n(selected.used_samples),
                  total: n(selected.sample_count),
                  attempts: n(selected.attempts),
                })
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.startedAt') }}</dt>
            <dd>
              <AppRelativeTime
                :instant="selected.started_at_ms || null"
                :locale="locale"
                empty-label="—"
                hint
              />
            </dd>
          </div>
          <div>
            <dt>{{ t('monitor.degradation.runs.duration') }}</dt>
            <dd>{{ durationText(selected.duration_ms) }}</dd>
          </div>
        </dl>

        <div v-if="selected.reasons.length" class="degradation-runs__reasons">
          <span
            v-for="label in reasonLabels(selected.reasons.join(','))"
            :key="label"
            class="degradation-runs__reason"
          >
            {{ label }}
          </span>
        </div>

        <p v-if="selected.error_code" class="degradation-runs__error">
          <strong>{{ tokenLabel(selected.error_code) }}</strong>
          <span v-if="selected.error_summary">{{ selected.error_summary }}</span>
        </p>

        <template v-if="ranking.length">
          <h4>{{ t('monitor.degradation.runs.ranking') }}</h4>
          <ul class="degradation-runs__ranking">
            <li
              v-for="entry in ranking"
              :key="entry.model"
              :class="{
                'degradation-runs__ranking-row--expected': entry.model === selected.expected_model,
              }"
            >
              <span class="degradation-runs__mono">
                {{
                  entry.display_name && entry.display_name !== entry.model
                    ? entry.display_name
                    : entry.model
                }}
              </span>
              <span class="degradation-runs__bar" aria-hidden="true">
                <span :style="{ width: barWidth(entry.probability_micros) }"></span>
              </span>
              <span class="degradation-runs__ranking-value">
                {{ probabilityLabel(entry.probability_micros) }}
              </span>
            </li>
          </ul>
        </template>

        <template v-if="selected.diagnostics.length">
          <h4>{{ t('monitor.degradation.runs.diagnostics') }}</h4>
          <ul class="degradation-runs__diagnostics">
            <li v-for="sample in selected.diagnostics" :key="sample.index">
              <StatusBadge :tone="sample.accepted ? 'success' : 'warning'" size="compact">
                {{ t('monitor.degradation.runs.sampleIndex', { index: n(sample.index + 1) }) }}
              </StatusBadge>
              <span>
                {{
                  t('monitor.degradation.runs.sampleNumbers', {
                    parsed: n(sample.parsed_numbers),
                    minimum: n(sample.minimum_numbers),
                  })
                }}
              </span>
            </li>
          </ul>
        </template>

        <div class="degradation-runs__output-heading">
          <h4>{{ t('monitor.degradation.runs.output') }}</h4>
          <CopyButton
            v-if="samples.length > 1"
            :value="allSamplesText"
            :label="t('monitor.degradation.runs.copyAll')"
            :success-label="t('monitor.degradation.runs.copySuccess')"
            :failure-label="t('monitor.degradation.runs.copyFailure')"
          />
        </div>
        <p v-if="samples.length === 0" class="degradation-runs__output-empty">
          {{ t('monitor.degradation.runs.outputEmpty') }}
        </p>
        <template v-else>
          <p class="degradation-runs__output-hint">
            {{ t('monitor.degradation.runs.outputHint') }}
          </p>
          <ul class="degradation-runs__outputs">
            <li v-for="sample in samples" :key="sample.index">
              <div class="degradation-runs__output-bar">
                <StatusBadge tone="neutral" size="compact">
                  {{ t('monitor.degradation.runs.sampleIndex', { index: n(sample.index + 1) }) }}
                </StatusBadge>
                <span class="degradation-runs__output-size">
                  {{ t('monitor.degradation.runs.outputChars', { value: n(sample.text.length) }) }}
                </span>
                <span v-if="sample.truncated" class="degradation-runs__output-truncated">
                  {{ t('monitor.degradation.runs.outputTruncated') }}
                </span>
                <CopyButton
                  :value="sample.text"
                  :label="t('monitor.degradation.runs.copySample', { index: n(sample.index + 1) })"
                  :success-label="t('monitor.degradation.runs.copySuccess')"
                  :failure-label="t('monitor.degradation.runs.copyFailure')"
                />
              </div>
              <pre class="degradation-runs__output-text">{{ sample.text }}</pre>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </AppDrawer>
</template>

<style scoped>
.degradation-runs {
  display: grid;
  min-width: 0;
  gap: 16px;
  padding: 16px 0;
}

.degradation-runs__stats {
  display: grid;
  gap: 8px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 10px 12px;
}

.degradation-runs__stats h3 {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.degradation-runs__stats h3 span {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

.degradation-runs__stats-row {
  display: flex;
  flex-wrap: wrap;
  margin: 0;
  gap: 6px 14px;
}

.degradation-runs__stats-row div {
  display: flex;
  align-items: baseline;
  gap: 5px;
}

.degradation-runs__stats-row dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.degradation-runs__stats-row dd {
  margin: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
  font-weight: 650;
}

.degradation-runs__stats-row--trigger dd {
  color: var(--color-text-muted);
  font-weight: 600;
}

.degradation-runs__stat--healthy {
  color: var(--color-success);
}

.degradation-runs__stat--degraded {
  color: var(--color-danger);
}

.degradation-runs__stat--inconclusive,
.degradation-runs__stat--quota_exhausted {
  color: var(--color-warning);
}

.degradation-runs__stats-hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}

.degradation-runs__list {
  display: grid;
  max-height: 240px;
  margin: 0;
  gap: 2px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 6px;
  overflow-y: auto;
  list-style: none;
}

.degradation-runs__entry {
  display: grid;
  width: 100%;
  align-items: center;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: var(--space-2);
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  background: none;
  color: inherit;
  padding: 6px 8px;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.degradation-runs__entry:hover {
  background: var(--color-surface);
}

.degradation-runs__entry--active {
  border-color: var(--color-action);
  background: var(--color-action-soft);
}

.degradation-runs__entry-meta {
  display: grid;
  min-width: 0;
  gap: 1px;
  color: var(--color-text);
  font-size: var(--text-sm);
}

.degradation-runs__entry-meta small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.degradation-runs__entry-probability {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}

.degradation-runs__detail {
  display: grid;
  min-width: 0;
  gap: 12px;
}

.degradation-runs__detail h3,
.degradation-runs__detail h4 {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 650;
}

.degradation-runs__facts {
  display: grid;
  margin: 0;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.degradation-runs__facts div {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.degradation-runs__facts dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.degradation-runs__facts dd {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
}

.degradation-runs__mono {
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.degradation-runs__value--success {
  color: var(--color-success);
  font-variant-numeric: tabular-nums;
}

.degradation-runs__value--danger {
  color: var(--color-danger);
  font-variant-numeric: tabular-nums;
}

.degradation-runs__reasons {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.degradation-runs__reason {
  border-radius: 999px;
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 2px 9px;
  font-size: var(--text-label-xs);
}

.degradation-runs__error {
  display: grid;
  margin: 0;
  gap: 2px;
  border-left: 2px solid var(--color-danger);
  background: var(--color-danger-bg);
  padding: 8px 10px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  line-height: 1.6;
}

.degradation-runs__error strong {
  color: var(--color-danger);
  font-family: var(--font-mono);
}

.degradation-runs__ranking,
.degradation-runs__diagnostics {
  display: grid;
  margin: 0;
  gap: 6px;
  padding: 0;
  list-style: none;
}

.degradation-runs__ranking li {
  display: grid;
  align-items: center;
  grid-template-columns: minmax(0, 1fr) minmax(60px, 1.1fr) auto;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.degradation-runs__ranking-row--expected {
  color: var(--color-text);
  font-weight: 620;
}

.degradation-runs__bar {
  display: block;
  height: 6px;
  border-radius: 999px;
  background: var(--color-surface-sunken);
  overflow: hidden;
}

.degradation-runs__bar > span {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--color-action);
}

.degradation-runs__ranking-value {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.degradation-runs__diagnostics li {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

.degradation-runs__output-heading {
  display: flex;
  min-height: 28px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.degradation-runs__output-hint,
.degradation-runs__output-empty {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}

.degradation-runs__outputs {
  display: grid;
  margin: 0;
  gap: 10px;
  padding: 0;
  list-style: none;
}

.degradation-runs__outputs li {
  display: grid;
  min-width: 0;
  gap: 6px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  padding: 8px 10px;
}

.degradation-runs__output-bar {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.degradation-runs__output-bar :deep(.copy-control) {
  margin-left: auto;
}

.degradation-runs__output-bar :deep(.copy-control button) {
  width: 32px;
  height: 32px;
}

.degradation-runs__output-size {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
}

.degradation-runs__output-truncated {
  border-radius: 999px;
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 2px 8px;
  font-size: var(--text-label-xs);
}

/* 输出经常上千字，这里只给一小块可滚动的预览，真正要用还是走复制。 */
.degradation-runs__output-text {
  max-height: 132px;
  margin: 0;
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 8px 10px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: 1.6;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (max-width: 520px) {
  .degradation-runs__facts {
    grid-template-columns: minmax(0, 1fr);
  }

  .degradation-runs__ranking li {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .degradation-runs__bar {
    grid-column: 1 / -1;
  }
}
</style>
