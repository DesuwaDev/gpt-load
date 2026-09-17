import {
  degradationStates,
  probabilityMicrosScale,
  type DegradationStateValue,
} from '@/app/resources/degradation'

/** 状态色板收窄到概览条也能接受的四档，StatusBadge 的 StatusTone 仍然兼容。 */
export type DegradationStateTone = 'neutral' | 'success' | 'warning' | 'danger'

/** 表格与概览条共用的状态次序：越靠前越需要人来处理。 */
export const degradationStateOrder: readonly DegradationStateValue[] = [
  'degraded',
  'error',
  'inconclusive',
  'quota_exhausted',
  'healthy',
  'unknown',
]

const stateTones: Record<DegradationStateValue, DegradationStateTone> = {
  degraded: 'danger',
  error: 'warning',
  inconclusive: 'warning',
  quota_exhausted: 'neutral',
  healthy: 'success',
  unknown: 'neutral',
}

export function degradationStateTone(state: DegradationStateValue): DegradationStateTone {
  return stateTones[state]
}

export function isDegradationState(value: unknown): value is DegradationStateValue {
  return degradationStates.includes(value as DegradationStateValue)
}

/** 周期下拉的候选值，两端与后端 [300, 604800] 的校验区间对齐。 */
export const degradationIntervalChoices = [
  300, 900, 1_800, 3_600, 10_800, 21_600, 43_200, 86_400, 259_200, 604_800,
] as const

export interface DurationParts {
  unit: 'minute' | 'hour' | 'day'
  value: number
}

/** 把秒数折成最大的整数单位，除不尽时退回下一级，避免出现 “0.5 天”。 */
export function durationParts(seconds: number): DurationParts {
  if (seconds >= 86_400 && seconds % 86_400 === 0) {
    return { unit: 'day', value: seconds / 86_400 }
  }
  if (seconds >= 3_600 && seconds % 3_600 === 0) {
    return { unit: 'hour', value: seconds / 3_600 }
  }
  return { unit: 'minute', value: Math.max(1, Math.round(seconds / 60)) }
}

export function probabilityPercent(micros: number): number {
  return (micros / probabilityMicrosScale) * 100
}

export function percentToMicros(percent: number): number {
  const micros = Math.round((percent / 100) * probabilityMicrosScale)
  return Math.min(probabilityMicrosScale, Math.max(1, micros))
}

export function formatProbability(micros: number, locale: string): string {
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(micros / probabilityMicrosScale)
}
