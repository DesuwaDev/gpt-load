import {
  degradationEfforts,
  degradationMaxIntervalSeconds,
  degradationMaxKeywords,
  degradationMaxNoteLength,
  degradationMinIntervalSeconds,
  type DegradationEffortValue,
  type DegradationMonitorDto,
  type DegradationMonitorUpdateRequest,
  type DegradationSettingsDto,
  type DegradationSettingsUpdateRequest,
} from '@/app/resources/degradation'

import { percentToMicros, probabilityPercent } from './degradation-presenter'

export interface NumberRange {
  min: number
  max: number
}

/** 与后端 CHECK 约束逐条对齐，表单先挡一次，省得把必然失败的请求发出去。 */
export const degradationRanges = {
  interval: { min: degradationMinIntervalSeconds, max: degradationMaxIntervalSeconds },
  sampleCount: { min: 1, max: 5 },
  probabilityPercent: { min: 0.1, max: 100 },
  concurrency: { min: 1, max: 16 },
  timeout: { min: 30, max: 1_800 },
  retry: { min: 0, max: 5 },
  quarantine: { min: 0, max: 100 },
  retention: { min: 1, max: 365 },
  overloadScan: { min: 5, max: 3_600 },
  overloadDebounce: { min: 0, max: 86_400 },
} as const satisfies Record<string, NumberRange>

export const degradationKeywordLimit = degradationMaxKeywords
export const degradationNoteLimit = degradationMaxNoteLength

/** 空串代表“继承全局”，所以解析必须把空和非法区分开。 */
export function parseIntegerField(value: string): number | undefined {
  const trimmed = value.trim()
  if (!/^\d{1,9}$/u.test(trimmed)) return undefined
  return Number(trimmed)
}

export function parsePercentField(value: string): number | undefined {
  const trimmed = value.trim()
  if (!/^\d{1,3}(?:\.\d{1,3})?$/u.test(trimmed)) return undefined
  return Number(trimmed)
}

function integerInRange(value: string, range: NumberRange): boolean {
  const parsed = parseIntegerField(value)
  return parsed !== undefined && parsed >= range.min && parsed <= range.max
}

function optionalIntegerInRange(value: string, range: NumberRange): boolean {
  return value.trim() === '' || integerInRange(value, range)
}

function percentInRange(value: string, range: NumberRange): boolean {
  const parsed = parsePercentField(value)
  return parsed !== undefined && parsed >= range.min && parsed <= range.max
}

function optionalPercentInRange(value: string, range: NumberRange): boolean {
  return value.trim() === '' || percentInRange(value, range)
}

export function splitKeywords(value: string): string[] {
  const parts = value
    .split(',')
    .map((part) => part.trim().toLocaleLowerCase())
    .filter((part) => part.length > 0)
  return [...new Set(parts)]
}

// ---------------------------------------------------------------------------
// 全局配置
// ---------------------------------------------------------------------------

export interface DegradationSettingsDraft {
  enabled: boolean
  interval_seconds: string
  cooldown_interval_seconds: string
  sample_count: string
  min_probability_percent: string
  run_when_disabled: boolean
  skip_quota_exhausted: boolean
  max_concurrent_runs: string
  request_timeout_seconds: string
  retry_limit: string
  quarantine_error_threshold: string
  history_retention_days: string
  overload_trigger_enabled: boolean
  overload_keywords: string
  overload_retry_limit: string
  overload_scan_interval_seconds: string
  overload_debounce_seconds: string
  notify_telegram_enabled: boolean
  notify_email_enabled: boolean
  notify_on_degraded: boolean
  notify_on_recovered: boolean
  notify_on_error: boolean
}

export type DegradationSettingsField = keyof DegradationSettingsDraft

export function settingsDraft(settings: DegradationSettingsDto): DegradationSettingsDraft {
  return {
    enabled: settings.enabled,
    interval_seconds: String(settings.interval_seconds),
    cooldown_interval_seconds: String(settings.cooldown_interval_seconds),
    sample_count: String(settings.sample_count),
    min_probability_percent: String(probabilityPercent(settings.min_probability_micros)),
    run_when_disabled: settings.run_when_disabled,
    skip_quota_exhausted: settings.skip_quota_exhausted,
    max_concurrent_runs: String(settings.max_concurrent_runs),
    request_timeout_seconds: String(settings.request_timeout_seconds),
    retry_limit: String(settings.retry_limit),
    quarantine_error_threshold: String(settings.quarantine_error_threshold),
    history_retention_days: String(settings.history_retention_days),
    overload_trigger_enabled: settings.overload_trigger_enabled,
    overload_keywords: settings.overload_keywords.join(', '),
    overload_retry_limit: String(settings.overload_retry_limit),
    overload_scan_interval_seconds: String(settings.overload_scan_interval_seconds),
    overload_debounce_seconds: String(settings.overload_debounce_seconds),
    notify_telegram_enabled: settings.notify_telegram_enabled,
    notify_email_enabled: settings.notify_email_enabled,
    notify_on_degraded: settings.notify_on_degraded,
    notify_on_recovered: settings.notify_on_recovered,
    notify_on_error: settings.notify_on_error,
  }
}

export function settingsDraftErrors(
  draft: DegradationSettingsDraft,
  maxSamples: number,
): Set<DegradationSettingsField> {
  const invalid = new Set<DegradationSettingsField>()
  const checks: [DegradationSettingsField, boolean][] = [
    ['interval_seconds', integerInRange(draft.interval_seconds, degradationRanges.interval)],
    [
      'cooldown_interval_seconds',
      integerInRange(draft.cooldown_interval_seconds, degradationRanges.interval),
    ],
    [
      'sample_count',
      integerInRange(draft.sample_count, { min: 1, max: maxSamples }),
    ],
    [
      'min_probability_percent',
      percentInRange(draft.min_probability_percent, degradationRanges.probabilityPercent),
    ],
    ['max_concurrent_runs', integerInRange(draft.max_concurrent_runs, degradationRanges.concurrency)],
    [
      'request_timeout_seconds',
      integerInRange(draft.request_timeout_seconds, degradationRanges.timeout),
    ],
    ['retry_limit', integerInRange(draft.retry_limit, degradationRanges.retry)],
    [
      'quarantine_error_threshold',
      integerInRange(draft.quarantine_error_threshold, degradationRanges.quarantine),
    ],
    [
      'history_retention_days',
      integerInRange(draft.history_retention_days, degradationRanges.retention),
    ],
    ['overload_retry_limit', integerInRange(draft.overload_retry_limit, degradationRanges.retry)],
    [
      'overload_scan_interval_seconds',
      integerInRange(draft.overload_scan_interval_seconds, degradationRanges.overloadScan),
    ],
    [
      'overload_debounce_seconds',
      integerInRange(draft.overload_debounce_seconds, degradationRanges.overloadDebounce),
    ],
  ]
  for (const [field, valid] of checks) {
    if (!valid) invalid.add(field)
  }
  const keywords = splitKeywords(draft.overload_keywords)
  if (
    keywords.length > degradationKeywordLimit ||
    keywords.join(',').length > 512 ||
    (draft.overload_trigger_enabled && keywords.length === 0)
  ) {
    invalid.add('overload_keywords')
  }
  return invalid
}

export function settingsPayload(
  draft: DegradationSettingsDraft,
): DegradationSettingsUpdateRequest {
  return {
    enabled: draft.enabled,
    interval_seconds: Number(draft.interval_seconds.trim()),
    cooldown_interval_seconds: Number(draft.cooldown_interval_seconds.trim()),
    sample_count: Number(draft.sample_count.trim()),
    min_probability_micros: percentToMicros(Number(draft.min_probability_percent.trim())),
    run_when_disabled: draft.run_when_disabled,
    skip_quota_exhausted: draft.skip_quota_exhausted,
    max_concurrent_runs: Number(draft.max_concurrent_runs.trim()),
    request_timeout_seconds: Number(draft.request_timeout_seconds.trim()),
    retry_limit: Number(draft.retry_limit.trim()),
    quarantine_error_threshold: Number(draft.quarantine_error_threshold.trim()),
    history_retention_days: Number(draft.history_retention_days.trim()),
    overload_trigger_enabled: draft.overload_trigger_enabled,
    overload_keywords: splitKeywords(draft.overload_keywords),
    overload_retry_limit: Number(draft.overload_retry_limit.trim()),
    overload_scan_interval_seconds: Number(draft.overload_scan_interval_seconds.trim()),
    overload_debounce_seconds: Number(draft.overload_debounce_seconds.trim()),
    notify_telegram_enabled: draft.notify_telegram_enabled,
    notify_email_enabled: draft.notify_email_enabled,
    notify_on_degraded: draft.notify_on_degraded,
    notify_on_recovered: draft.notify_on_recovered,
    notify_on_error: draft.notify_on_error,
  }
}

// ---------------------------------------------------------------------------
// 单个监控项 / 批量加入共用的检测参数
// ---------------------------------------------------------------------------

/** 三态覆盖：inherit 表示跟随全局，其余两个值固定住行为。 */
export type DegradationInheritable = 'inherit' | 'on' | 'off'

export interface DegradationProbeDraft {
  upstream_model: string
  expected_model: string
  reasoning_effort: DegradationEffortValue
  sample_count: string
  min_probability_percent: string
  interval_seconds: string
  cooldown_interval_seconds: string
  run_when_disabled: DegradationInheritable
  note: string
}

export type DegradationProbeField = keyof DegradationProbeDraft

export function emptyProbeDraft(): DegradationProbeDraft {
  return {
    upstream_model: '',
    expected_model: '',
    reasoning_effort: '',
    sample_count: '',
    min_probability_percent: '',
    interval_seconds: '',
    cooldown_interval_seconds: '',
    run_when_disabled: 'inherit',
    note: '',
  }
}

export function monitorProbeDraft(monitor: DegradationMonitorDto): DegradationProbeDraft {
  return {
    upstream_model: monitor.upstream_model,
    expected_model: monitor.expected_model,
    reasoning_effort: monitor.reasoning_effort,
    sample_count: monitor.sample_count === 0 ? '' : String(monitor.sample_count),
    min_probability_percent:
      monitor.min_probability_micros === 0
        ? ''
        : String(probabilityPercent(monitor.min_probability_micros)),
    interval_seconds: monitor.interval_seconds === 0 ? '' : String(monitor.interval_seconds),
    cooldown_interval_seconds:
      monitor.cooldown_interval_seconds === 0 ? '' : String(monitor.cooldown_interval_seconds),
    run_when_disabled:
      monitor.run_when_disabled === null ? 'inherit' : monitor.run_when_disabled ? 'on' : 'off',
    note: monitor.note,
  }
}

/** 表单控件统一发 string，这里按字段收敛回联合类型，避免各处重复断言。 */
export function applyProbeField(
  draft: DegradationProbeDraft,
  field: DegradationProbeField,
  value: string,
): void {
  if (field === 'reasoning_effort') {
    draft.reasoning_effort = degradationEfforts.includes(value as DegradationEffortValue)
      ? (value as DegradationEffortValue)
      : ''
    return
  }
  if (field === 'run_when_disabled') {
    draft.run_when_disabled = value === 'on' || value === 'off' ? value : 'inherit'
    return
  }
  draft[field] = value
}

export function probeDraftErrors(
  draft: DegradationProbeDraft,
  maxSamples: number,
): Set<DegradationProbeField> {
  const invalid = new Set<DegradationProbeField>()
  const model = draft.upstream_model.trim()
  if (model === '' || model.length > 255) invalid.add('upstream_model')
  if (draft.expected_model.trim() === '') invalid.add('expected_model')
  if (!optionalIntegerInRange(draft.sample_count, { min: 1, max: maxSamples })) {
    invalid.add('sample_count')
  }
  if (
    !optionalPercentInRange(draft.min_probability_percent, degradationRanges.probabilityPercent)
  ) {
    invalid.add('min_probability_percent')
  }
  if (!optionalIntegerInRange(draft.interval_seconds, degradationRanges.interval)) {
    invalid.add('interval_seconds')
  }
  if (!optionalIntegerInRange(draft.cooldown_interval_seconds, degradationRanges.interval)) {
    invalid.add('cooldown_interval_seconds')
  }
  if (draft.note.trim().length > degradationNoteLimit) invalid.add('note')
  return invalid
}

function overrideInteger(value: string): number {
  const parsed = parseIntegerField(value)
  return parsed ?? 0
}

function overrideMicros(value: string): number {
  const parsed = parsePercentField(value)
  return parsed === undefined ? 0 : percentToMicros(parsed)
}

export function probeOverrides(draft: DegradationProbeDraft): {
  upstream_model: string
  expected_model: string
  reasoning_effort: DegradationEffortValue
  sample_count: number
  min_probability_micros: number
  interval_seconds: number
  cooldown_interval_seconds: number
  run_when_disabled: boolean | null
  note: string
} {
  return {
    upstream_model: draft.upstream_model.trim(),
    expected_model: draft.expected_model.trim(),
    reasoning_effort: draft.reasoning_effort,
    sample_count: overrideInteger(draft.sample_count),
    min_probability_micros: overrideMicros(draft.min_probability_percent),
    interval_seconds: overrideInteger(draft.interval_seconds),
    cooldown_interval_seconds: overrideInteger(draft.cooldown_interval_seconds),
    run_when_disabled:
      draft.run_when_disabled === 'inherit' ? null : draft.run_when_disabled === 'on',
    note: draft.note.trim(),
  }
}

export function monitorUpdatePayload(
  draft: DegradationProbeDraft,
  enabled: boolean,
): DegradationMonitorUpdateRequest {
  return { ...probeOverrides(draft), enabled }
}
