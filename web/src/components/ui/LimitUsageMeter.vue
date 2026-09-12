<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import { quotaProgressTone } from '@/lib/quota-progress'

// 限额实时用量：已用/上限 + 进度条。上限为 0 时显示 ∞ 并省略进度条，
// 因为“无限”没有可填充的比例，画一条空槽反而误导。
const props = defineProps<{
  label: string
  used: number
  limit: number
  compact?: boolean
}>()
const { n } = useI18n()

const unlimited = computed(() => props.limit <= 0)
// 进度条统一按“剩余百分比”着色，与费用额度条的红黄绿口径一致。
const remainingPercent = computed(() => {
  if (unlimited.value) return 100
  return Math.max(0, Math.min(100, ((props.limit - props.used) / props.limit) * 100))
})
const tone = computed(() =>
  quotaProgressTone(remainingPercent.value, !unlimited.value && props.used >= props.limit),
)
const text = computed(() => `${n(props.used)}/${unlimited.value ? '∞' : n(props.limit)}`)
</script>

<template>
  <span class="limit-usage" :class="{ 'limit-usage--compact': compact }">
    <span class="limit-usage__label">{{ label }}</span>
    <span class="limit-usage__value" :class="`limit-usage__value--${tone}`">{{ text }}</span>
    <QuotaProgressBar
      v-if="!unlimited"
      class="limit-usage__bar"
      :value="remainingPercent"
      :tone="tone"
      :label="label"
      :value-text="text"
      compact
    />
  </span>
</template>

<style scoped>
.limit-usage {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: 2px 6px;
}
.limit-usage__label {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.limit-usage__value {
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  font-weight: 560;
  text-align: right;
}
.limit-usage__value--warning {
  color: var(--color-warning);
}
.limit-usage__value--danger {
  color: var(--color-danger);
}
.limit-usage__bar {
  grid-column: 1 / -1;
}
.limit-usage--compact {
  gap: 1px 5px;
}
</style>
