<script setup lang="ts">
import { CircleCheck, CircleOff, ListChecks, Radar, RotateCcw, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

defineProps<{
  selectedCount: number
  allVisibleSelected: boolean
  canSelectAll: boolean
  pending?: boolean
}>()
const emit = defineEmits<{
  'toggle-select': []
  run: []
  enable: []
  disable: []
  clear: []
  remove: []
}>()
const { n, t } = useI18n()
</script>

<template>
  <div class="degradation-batch">
    <div class="degradation-batch__actions">
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        :disabled="!canSelectAll"
        @click="emit('toggle-select')"
      >
        <ListChecks :size="15" aria-hidden="true" />
        {{
          allVisibleSelected
            ? t('monitor.degradation.batch.clearAll')
            : t('monitor.degradation.batch.selectAll')
        }}
        <span v-if="selectedCount > 0" class="degradation-batch__count">{{ n(selectedCount) }}</span>
      </AppButton>
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
        @click="emit('run')"
      >
        <Radar :size="15" aria-hidden="true" />
        {{ t('monitor.degradation.batch.run') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="success"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
        @click="emit('enable')"
      >
        <CircleCheck :size="15" aria-hidden="true" />
        {{ t('monitor.degradation.batch.enable') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="warning"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
        @click="emit('disable')"
      >
        <CircleOff :size="15" aria-hidden="true" />
        {{ t('monitor.degradation.batch.disable') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
        @click="emit('clear')"
      >
        <RotateCcw :size="15" aria-hidden="true" />
        {{ t('monitor.degradation.batch.clear') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="danger"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
        @click="emit('remove')"
      >
        <Trash2 :size="15" aria-hidden="true" />
        {{ t('monitor.degradation.batch.delete') }}
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.degradation-batch {
  display: flex;
  justify-self: end;
  width: max-content;
  max-width: 100%;
  min-width: 0;
  border: 0;
  background: transparent;
  padding: 0;
}

.degradation-batch__count {
  display: inline-flex;
  min-width: 1.25em;
  height: 1.25em;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: color-mix(in srgb, currentColor 16%, transparent);
  padding: 0 4px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 650;
}

.degradation-batch__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}

@media (max-width: 800px) {
  .degradation-batch {
    justify-self: stretch;
    width: auto;
  }

  .degradation-batch__actions {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .degradation-batch__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
