import { useI18n } from 'vue-i18n'

import type {
  DegradationEffortValue,
  DegradationStateValue,
  DegradationTriggerValue,
} from '@/app/resources/degradation'

import { durationParts, formatProbability } from './degradation-presenter'

/**
 * 监控页各处共用的降智文案。
 *
 * state_reason 把判定原因、跳过原因和错误码混在同一个逗号串里，因此三套词表
 * 合并成 `monitor.degradation.reason.*` 一个命名空间；后端将来新增 token 时
 * 回退成原文，不会炸出缺 key 的告警。
 */
export function useDegradationLabels() {
  const { locale, n, t, te } = useI18n()

  function durationLabel(seconds: number): string {
    const parts = durationParts(seconds)
    return t(`monitor.degradation.duration.${parts.unit}`, { value: n(parts.value) })
  }

  function stateLabel(state: DegradationStateValue): string {
    return t(`monitor.degradation.state.${state}`)
  }

  function triggerLabel(trigger: DegradationTriggerValue): string {
    return t(`monitor.degradation.trigger.${trigger}`)
  }

  function effortLabel(effort: DegradationEffortValue): string {
    return t(`monitor.degradation.effort.${effort === '' ? 'default' : effort}`)
  }

  function tokenLabel(token: string): string {
    const key = `monitor.degradation.reason.${token}`
    return te(key) ? t(key) : token
  }

  function reasonLabels(reason: string): string[] {
    return reason
      .split(',')
      .map((token) => token.trim())
      .filter((token) => token.length > 0)
      .map(tokenLabel)
  }

  function probabilityLabel(micros: number): string {
    return formatProbability(micros, locale.value)
  }

  return {
    durationLabel,
    effortLabel,
    probabilityLabel,
    reasonLabels,
    stateLabel,
    tokenLabel,
    triggerLabel,
  }
}
