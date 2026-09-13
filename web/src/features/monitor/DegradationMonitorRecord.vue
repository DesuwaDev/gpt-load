<script setup lang="ts">
import { PencilLine, Radar, ScrollText, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { DegradationMonitorDto } from '@/app/resources/degradation'
import { groupDetailLocation } from '@/app/route-locations'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import IconButton from '@/components/ui/IconButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import { degradationStateTone } from './degradation-presenter'
import { useDegradationLabels } from './use-degradation-labels'

const props = defineProps<{
  item: DegradationMonitorDto
  selected: boolean
  busy: boolean
  rowIndex: number
}>()
const emit = defineEmits<{
  'update:selected': [selected: boolean]
  run: [monitor: DegradationMonitorDto]
  edit: [monitor: DegradationMonitorDto]
  history: [monitor: DegradationMonitorDto]
  remove: [monitor: DegradationMonitorDto]
}>()
const { locale, t } = useI18n()
const { durationLabel, effortLabel, probabilityLabel, reasonLabels, stateLabel } =
  useDegradationLabels()

const targetLabel = computed(() =>
  props.item.credential_id > 0 ? props.item.credential_mask : props.item.group_name,
)
const targetMeta = computed(() => {
  const parts = [
    props.item.credential_id > 0
      ? props.item.group_name
      : t('monitor.degradation.record.wholeGroup'),
    props.item.channel_id,
  ]
  if (!props.item.group_enabled) parts.push(t('monitor.degradation.record.groupDisabled'))
  return parts.join(' · ')
})
const probeMeta = computed(() =>
  [
    props.item.expected_model_name || props.item.expected_model,
    effortLabel(props.item.reasoning_effort),
  ].join(' · '),
)
const reasons = computed(() => reasonLabels(props.item.state_reason))
const reasonText = computed(() => reasons.value.join(' · '))
const detectedLabel = computed(() => {
  const item = props.item
  if (item.last_detected_model === '') return ''
  const name = item.last_detected_model_name || item.last_detected_model
  return t('monitor.degradation.record.detected', { model: name })
})
const probabilityMet = computed(
  () =>
    props.item.last_probability_micros > 0 &&
    props.item.last_probability_micros >= props.item.effective_min_probability_micros,
)
const scheduleMeta = computed(() => {
  const item = props.item
  const interval = durationLabel(
    item.state === 'healthy' ? item.effective_interval_seconds : item.effective_cooldown_interval_seconds,
  )
  return item.state === 'healthy'
    ? t('monitor.degradation.record.everyInterval', { value: interval })
    : t('monitor.degradation.record.everyCooldown', { value: interval })
})
</script>

<template>
  <article
    class="ledger-record-list__record degradation-record"
    :class="{ 'degradation-record--paused': !item.enabled }"
    role="row"
    :aria-rowindex="rowIndex"
  >
    <div class="ledger-record-list__cell degradation-record__select" role="cell">
      <label>
        <span class="sr-only">
          {{ t('monitor.degradation.record.selectMonitor', { target: targetLabel }) }}
        </span>
        <input
          type="checkbox"
          :checked="selected"
          :disabled="busy"
          @change="emit('update:selected', ($event.target as HTMLInputElement).checked)"
        />
      </label>
    </div>

    <div class="ledger-record-list__cell degradation-record__target" role="cell">
      <OverflowTooltip
        :as="RouterLink"
        class="degradation-record__identity"
        :content="targetLabel"
        :to="groupDetailLocation(item.group_id, { tab: 'credentials' })"
      >
        {{ targetLabel }}
      </OverflowTooltip>
      <OverflowTooltip as="small" :content="targetMeta">{{ targetMeta }}</OverflowTooltip>
    </div>

    <div class="ledger-record-list__cell degradation-record__probe" role="cell">
      <OverflowTooltip as="span" class="degradation-record__model" :content="item.upstream_model">
        {{ item.upstream_model }}
      </OverflowTooltip>
      <OverflowTooltip as="small" :content="probeMeta">{{ probeMeta }}</OverflowTooltip>
    </div>

    <div class="ledger-record-list__cell degradation-record__state" role="cell">
      <StatusBadge :tone="degradationStateTone(item.state)" size="compact">
        {{ stateLabel(item.state) }}
      </StatusBadge>
      <OverflowTooltip v-if="reasonText" as="small" :content="reasonText">
        {{ reasonText }}
      </OverflowTooltip>
      <OverflowTooltip v-else-if="!item.enabled" as="small" :content="t('monitor.degradation.record.paused')">
        {{ t('monitor.degradation.record.paused') }}
      </OverflowTooltip>
    </div>

    <div class="ledger-record-list__cell degradation-record__probability" role="cell">
      <span
        v-if="item.last_probability_micros > 0"
        :class="
          probabilityMet
            ? 'degradation-record__probability-value--success'
            : 'degradation-record__probability-value--danger'
        "
      >
        {{ probabilityLabel(item.last_probability_micros) }}
      </span>
      <span v-else class="degradation-record__probability-empty">—</span>
      <OverflowTooltip
        as="small"
        :content="
          detectedLabel ||
          t('monitor.degradation.record.threshold', {
            value: probabilityLabel(item.effective_min_probability_micros),
          })
        "
      >
        {{
          detectedLabel ||
          t('monitor.degradation.record.threshold', {
            value: probabilityLabel(item.effective_min_probability_micros),
          })
        }}
      </OverflowTooltip>
    </div>

    <div class="ledger-record-list__cell degradation-record__schedule" role="cell">
      <AppRelativeTime
        :instant="item.enabled && item.next_run_at_ms > 0 ? item.next_run_at_ms : null"
        :locale="locale"
        :empty-label="item.enabled ? '—' : t('monitor.degradation.record.paused')"
        hint
      />
      <OverflowTooltip as="small" :content="scheduleMeta">{{ scheduleMeta }}</OverflowTooltip>
    </div>

    <div class="ledger-record-list__cell degradation-record__actions" role="cell">
      <IconButton
        variant="ghost"
        size="compact"
        :busy="busy"
        :disabled="busy"
        :label="t('monitor.degradation.record.runNow', { target: targetLabel })"
        @click="emit('run', item)"
      >
        <Radar :size="15" aria-hidden="true" />
      </IconButton>
      <IconButton
        variant="ghost"
        size="compact"
        :disabled="busy"
        :label="t('monitor.degradation.record.editMonitor', { target: targetLabel })"
        @click="emit('edit', item)"
      >
        <PencilLine :size="15" aria-hidden="true" />
      </IconButton>
      <IconButton
        variant="ghost"
        size="compact"
        :disabled="busy"
        :label="t('monitor.degradation.record.viewRuns', { target: targetLabel })"
        @click="emit('history', item)"
      >
        <ScrollText :size="15" aria-hidden="true" />
      </IconButton>
      <IconButton
        variant="ghost"
        tone="danger"
        size="compact"
        :disabled="busy"
        :label="t('monitor.degradation.record.removeMonitor', { target: targetLabel })"
        @click="emit('remove', item)"
      >
        <Trash2 :size="15" aria-hidden="true" />
      </IconButton>
    </div>
  </article>
</template>

<style scoped>
.degradation-record {
  --ledger-record-list-record-min-height: 76px;
  --ledger-record-list-record-padding: 10px 0;
}

.degradation-record--paused {
  background: color-mix(in srgb, var(--color-surface-sunken) 55%, var(--color-surface));
}

.degradation-record__select {
  display: flex;
  justify-content: center;
}

.degradation-record__select label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}

.degradation-record__select input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}

.degradation-record__target,
.degradation-record__probe,
.degradation-record__state,
.degradation-record__probability,
.degradation-record__schedule {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  flex-direction: column;
  gap: var(--space-1);
}

.degradation-record__identity,
.degradation-record__model {
  max-width: 100%;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: 620;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.degradation-record__identity:hover {
  color: var(--color-action);
}

.degradation-record small {
  width: 100%;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.degradation-record__probability > span:first-child {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-variant-numeric: tabular-nums;
}

.degradation-record__probability-value--success {
  color: var(--color-success);
}

.degradation-record__probability-value--danger {
  color: var(--color-danger);
}

.degradation-record__probability-empty {
  color: var(--color-text-faint);
}

.degradation-record__schedule {
  color: var(--color-text);
  font-size: var(--text-sm);
}

.degradation-record__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 2px;
}

@media (max-width: 860px) {
  .degradation-record {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .degradation-record__select {
    position: absolute;
    top: 2px;
    right: 4px;
    justify-content: flex-end;
  }

  .degradation-record__select label {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .degradation-record__target,
  .degradation-record__probe,
  .degradation-record__probability,
  .degradation-record__schedule {
    grid-column: 1 / -1;
  }

  .degradation-record__state {
    grid-column: 1 / -1;
    padding-right: var(--touch-target);
  }

  .degradation-record__actions {
    grid-column: 1 / -1;
    justify-content: flex-start;
  }
}
</style>
