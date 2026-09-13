<script setup lang="ts">
import { TrendingDown } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import EmptyState from '@/components/ui/EmptyState.vue'

import MonitorSectionHeading from './MonitorSectionHeading.vue'

const { t } = useI18n()
</script>

<template>
  <section class="degradation-tab" aria-labelledby="degradation-title">
    <MonitorSectionHeading
      id="degradation-title"
      :title="t('monitor.degradation.title')"
      :description="t('monitor.degradation.description')"
    />
    <!-- 检测本身还没接，先把分区的原生外壳占住，接入时只往里填内容、不用再动布局。 -->
    <EmptyState
      class="degradation-empty"
      variant="ledger"
      heading-as="h3"
      :title="t('monitor.degradation.empty.title')"
      :description="t('monitor.degradation.empty.description')"
    >
      <template #icon><TrendingDown :size="22" stroke-width="1.7" /></template>
    </EmptyState>
  </section>
</template>

<style scoped>
.degradation-tab {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

/* 与路由检查的空态同款，避免两个相邻分区一个高一个矮。 */
.degradation-empty {
  min-height: 330px;
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-card);
}
</style>
