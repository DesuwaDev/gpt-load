<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import { quotaProgressTone } from '@/lib/quota-progress'

// 限额实时用量：已用/上限 + 进度条。上限为 0 时限流器压根不记账（Acquire 在
// limit <= 0 时直接放行），已用数恒为 0，所以不写“0/∞”这种假测量值，
// 只留 ∞ 并把进度条按满格的独立色画出来，表示“没有上限”而不是“还剩很多”。
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
  unlimited.value
    ? ('unlimited' as const)
    : quotaProgressTone(remainingPercent.value, props.used >= props.limit),
)
const text = computed(() => (unlimited.value ? '∞' : `${n(props.used)}/${n(props.limit)}`))
</script>

<template>
  <span class="limit-usage" :class="{ 'limit-usage--compact': compact }">
    <span class="limit-usage__label">{{ label }}</span>
    <span class="limit-usage__value" :class="`limit-usage__value--${tone}`">{{ text }}</span>
    <QuotaProgressBar
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
