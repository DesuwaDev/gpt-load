import { keepPreviousData, queryOptions } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys, normalizeDegradationMonitorFilters } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export const degradationStates = [
  'unknown',
  'healthy',
  'degraded',
  'inconclusive',
  'error',
  'quota_exhausted',
] as const
export type DegradationStateValue = (typeof degradationStates)[number]

/** 空字符串表示「跟随上游默认」，与后端 DegradationEffortDefault 对齐。 */
export const degradationEfforts = ['', 'minimal', 'low', 'medium', 'high'] as const
export type DegradationEffortValue = (typeof degradationEfforts)[number]

export const degradationTriggers = ['schedule', 'manual', 'overload'] as const
export type DegradationTriggerValue = (typeof degradationTriggers)[number]

export const degradationBatchActions = ['enable', 'disable', 'delete', 'clear', 'run'] as const
export type DegradationBatchActionValue = (typeof degradationBatchActions)[number]

/** 归因概率以百万分之一为单位存储，避免浮点数在前后端之间来回失真。 */
export const probabilityMicrosScale = 1_000_000
export const degradationRunHistoryLimit = 50
export const degradationMaxEnrollTargets = 500
export const degradationMaxBatchMonitors = 500
export const degradationMinIntervalSeconds = 300
export const degradationMaxIntervalSeconds = 604_800
export const degradationMaxNoteLength = 255
export const degradationMaxKeywords = 16
/** 还有检测排队或在执行时，监控页按这个节奏轮询，跑完即停。 */
export const degradationActivePollIntervalMs = 4_000

export interface DegradationSettingsDto {
  enabled: boolean
  interval_seconds: number
  cooldown_interval_seconds: number
  sample_count: number
  min_probability_micros: number
  run_when_disabled: boolean
  skip_quota_exhausted: boolean
  max_concurrent_runs: number
  request_timeout_seconds: number
  retry_limit: number
  quarantine_error_threshold: number
  history_retention_days: number
  overload_trigger_enabled: boolean
  overload_keywords: string[]
  overload_retry_limit: number
  overload_scan_interval_seconds: number
  overload_debounce_seconds: number
  notify_telegram_enabled: boolean
  notify_email_enabled: boolean
  notify_on_degraded: boolean
  notify_on_recovered: boolean
  notify_on_error: boolean
  updated_at_ms: number
}

export type DegradationSettingsUpdateRequest = Partial<Omit<DegradationSettingsDto, 'updated_at_ms'>>

export interface DegradationBankModelDto {
  id: string
  display_name: string
  family: string
  family_name: string
}

export interface DegradationCatalogDto {
  models: DegradationBankModelDto[]
  efforts: DegradationEffortValue[]
  states: DegradationStateValue[]
  recommended_samples: number
  max_samples: number
  calibrated_samples: number
  method: string
  built_at: string
}

export interface DegradationOverviewDto {
  settings: DegradationSettingsDto
  catalog: DegradationCatalogDto
}

export interface DegradationSummaryDto {
  total: number
  enabled: number
  healthy: number
  degraded: number
  inconclusive: number
  error: number
  quota_exhausted: number
  unknown: number
}

export interface DegradationMonitorDto {
  id: number
  group_id: number
  group_name: string
  group_enabled: boolean
  channel_id: string
  connection_type: string
  credential_id: number
  credential_mask: string
  credential_status: string
  /** 套餐标记与分组页同源，凭据观测缺失时为空串。 */
  plan_name: string
  plan_level: string
  upstream_model: string
  expected_model: string
  expected_model_name: string
  reasoning_effort: DegradationEffortValue
  sample_count: number
  min_probability_micros: number
  interval_seconds: number
  cooldown_interval_seconds: number
  run_when_disabled: boolean | null
  enabled: boolean
  state: DegradationStateValue
  state_reason: string
  state_since_ms: number
  last_run_at_ms: number
  last_success_at_ms: number
  next_run_at_ms: number
  last_probability_micros: number
  last_detected_model: string
  last_detected_model_name: string
  last_error_code: string
  consecutive_errors: number
  note: string
  effective_sample_count: number
  effective_min_probability_micros: number
  effective_interval_seconds: number
  effective_cooldown_interval_seconds: number
  effective_run_when_disabled: boolean
  created_at_ms: number
  updated_at_ms: number
}

export interface DegradationPaginationDto {
  page: number
  page_size: 20 | 50 | 100
  total_items: number
  total_pages: number
}

export interface DegradationMonitorCollectionDto {
  observed_at_ms: number
  settings: DegradationSettingsDto
  summary: DegradationSummaryDto
  items: DegradationMonitorDto[]
  pagination: DegradationPaginationDto
  /** 本实例上排队或正在执行的检测条数，仅用于判断要不要继续轮询。 */
  active_runs: number
}

export interface DegradationMonitorFilters {
  query?: string
  state?: DegradationStateValue
  group_id?: number
  enabled?: boolean
  page: number
  page_size: 20 | 50 | 100
}

export interface DegradationTargetRequest {
  group_id: number
  credential_id: number
}

export interface DegradationEnrollRequest {
  targets: DegradationTargetRequest[]
  upstream_model: string
  expected_model: string
  reasoning_effort: DegradationEffortValue
  sample_count: number
  min_probability_micros: number
  interval_seconds: number
  cooldown_interval_seconds: number
  run_when_disabled: boolean | null
  note: string
  skip_existing: boolean
}

export interface DegradationRejectedTargetDto {
  group_id: number
  credential_id: number
  reason: string
}

export interface DegradationEnrollResultDto {
  created: number
  updated: number
  skipped: number
  rejected: DegradationRejectedTargetDto[]
  items: DegradationMonitorDto[]
}

export interface DegradationMonitorUpdateRequest {
  upstream_model?: string
  expected_model?: string
  reasoning_effort?: DegradationEffortValue
  sample_count?: number
  min_probability_micros?: number
  interval_seconds?: number
  cooldown_interval_seconds?: number
  run_when_disabled?: boolean | null
  enabled?: boolean
  note?: string
}

export interface DegradationBatchRequest {
  action: DegradationBatchActionValue
  monitor_ids: number[]
}

export interface DegradationBatchResultDto {
  action: DegradationBatchActionValue
  affected: number
}

export interface DegradationRankingEntryDto {
  model: string
  display_name: string
  family: string
  probability_micros: number
}

export interface DegradationSampleDiagnosticDto {
  index: number
  parsed_numbers: number
  minimum_numbers: number
  accepted: boolean
}

export interface DegradationSampleTextDto {
  index: number
  expected_count: number
  text: string
  truncated: boolean
}

export interface DegradationRunDto {
  id: number
  monitor_id: number
  trigger: DegradationTriggerValue
  outcome: DegradationStateValue
  started_at_ms: number
  completed_at_ms: number
  duration_ms: number
  expected_model: string
  detected_model: string
  detected_model_name: string
  expected_probability_micros: number
  leading_probability_micros: number
  min_probability_micros: number
  sample_count: number
  used_samples: number
  attempts: number
  reasons: string[]
  error_code: string
  error_summary: string
  ranking: DegradationRankingEntryDto[]
  diagnostics: DegradationSampleDiagnosticDto[]
  /** 这次检测收到的上游原文，供人工复核，界面只提供复制。 */
  samples: DegradationSampleTextDto[]
}

export interface DegradationRunStatsDto {
  total: number
  healthy: number
  degraded: number
  inconclusive: number
  error: number
  quota_exhausted: number
  unknown: number
  schedule: number
  manual: number
  overload: number
}

export interface DegradationRunCollectionDto {
  monitor_id: number
  items: DegradationRunDto[]
  /** 统计的是这条监控保留的全部历史，不随本次返回的条数变化。 */
  stats: DegradationRunStatsDto
}

const settingsFields = [
  'enabled',
  'interval_seconds',
  'cooldown_interval_seconds',
  'sample_count',
  'min_probability_micros',
  'run_when_disabled',
  'skip_quota_exhausted',
  'max_concurrent_runs',
  'request_timeout_seconds',
  'retry_limit',
  'quarantine_error_threshold',
  'history_retention_days',
  'overload_trigger_enabled',
  'overload_keywords',
  'overload_retry_limit',
  'overload_scan_interval_seconds',
  'overload_debounce_seconds',
  'notify_telegram_enabled',
  'notify_email_enabled',
  'notify_on_degraded',
  'notify_on_recovered',
  'notify_on_error',
  'updated_at_ms',
] as const
const settingsUpdateFields = settingsFields.filter(
  (field): field is Exclude<(typeof settingsFields)[number], 'updated_at_ms'> =>
    field !== 'updated_at_ms',
)
const bankModelFields = ['id', 'display_name', 'family', 'family_name'] as const
const catalogFields = [
  'models',
  'efforts',
  'states',
  'recommended_samples',
  'max_samples',
  'calibrated_samples',
  'method',
  'built_at',
] as const
const overviewFields = ['settings', 'catalog'] as const
const summaryFields = [
  'total',
  'enabled',
  'healthy',
  'degraded',
  'inconclusive',
  'error',
  'quota_exhausted',
  'unknown',
] as const
const monitorFields = [
  'id',
  'group_id',
  'group_name',
  'group_enabled',
  'channel_id',
  'connection_type',
  'credential_id',
  'credential_mask',
  'credential_status',
  'plan_name',
  'plan_level',
  'upstream_model',
  'expected_model',
  'expected_model_name',
  'reasoning_effort',
  'sample_count',
  'min_probability_micros',
  'interval_seconds',
  'cooldown_interval_seconds',
  'run_when_disabled',
  'enabled',
  'state',
  'state_reason',
  'state_since_ms',
  'last_run_at_ms',
  'last_success_at_ms',
  'next_run_at_ms',
  'last_probability_micros',
  'last_detected_model',
  'last_detected_model_name',
  'last_error_code',
  'consecutive_errors',
  'note',
  'effective_sample_count',
  'effective_min_probability_micros',
  'effective_interval_seconds',
  'effective_cooldown_interval_seconds',
  'effective_run_when_disabled',
  'created_at_ms',
  'updated_at_ms',
] as const
const paginationFields = ['page', 'page_size', 'total_items', 'total_pages'] as const
const collectionFields = [
  'observed_at_ms',
  'settings',
  'summary',
  'items',
  'pagination',
  'active_runs',
] as const
const rejectedFields = ['group_id', 'credential_id', 'reason'] as const
const enrollResultFields = ['created', 'updated', 'skipped', 'rejected', 'items'] as const
const batchResultFields = ['action', 'affected'] as const
const rankingFields = ['model', 'display_name', 'family', 'probability_micros'] as const
const diagnosticFields = ['index', 'parsed_numbers', 'minimum_numbers', 'accepted'] as const
const sampleTextFields = ['index', 'expected_count', 'text', 'truncated'] as const
const runStatsFields = [
  'total',
  'healthy',
  'degraded',
  'inconclusive',
  'error',
  'quota_exhausted',
  'unknown',
  'schedule',
  'manual',
  'overload',
] as const
const runFields = [
  'id',
  'monitor_id',
  'trigger',
  'outcome',
  'started_at_ms',
  'completed_at_ms',
  'duration_ms',
  'expected_model',
  'detected_model',
  'detected_model_name',
  'expected_probability_micros',
  'leading_probability_micros',
  'min_probability_micros',
  'sample_count',
  'used_samples',
  'attempts',
  'reasons',
  'error_code',
  'error_summary',
  'ranking',
  'diagnostics',
  'samples',
] as const
const runCollectionFields = ['monitor_id', 'items', 'stats'] as const
const enrollFields = [
  'targets',
  'upstream_model',
  'expected_model',
  'reasoning_effort',
  'sample_count',
  'min_probability_micros',
  'interval_seconds',
  'cooldown_interval_seconds',
  'run_when_disabled',
  'note',
  'skip_existing',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectFreeString(value: unknown): string {
  return projectString(value, { allowEmpty: true })
}

function projectProbabilityMicros(value: unknown): number {
  return projectSafeInteger(value, { minimum: 0, maximum: probabilityMicrosScale })
}

export function projectDegradationSettings(value: unknown): DegradationSettingsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, settingsFields)
  const keywords = projectArray(record.overload_keywords, projectString)
  if (
    keywords.length > degradationMaxKeywords ||
    new Set(keywords).size !== keywords.length ||
    keywords.some((keyword) => keyword !== keyword.trim())
  ) {
    invalidResponse()
  }
  return {
    enabled: projectBoolean(record.enabled),
    interval_seconds: projectSafeInteger(record.interval_seconds, {
      minimum: degradationMinIntervalSeconds,
      maximum: degradationMaxIntervalSeconds,
    }),
    cooldown_interval_seconds: projectSafeInteger(record.cooldown_interval_seconds, {
      minimum: degradationMinIntervalSeconds,
      maximum: degradationMaxIntervalSeconds,
    }),
    sample_count: projectSafeInteger(record.sample_count, { minimum: 1, maximum: 5 }),
    min_probability_micros: projectSafeInteger(record.min_probability_micros, {
      minimum: 1,
      maximum: probabilityMicrosScale,
    }),
    run_when_disabled: projectBoolean(record.run_when_disabled),
    skip_quota_exhausted: projectBoolean(record.skip_quota_exhausted),
    max_concurrent_runs: projectSafeInteger(record.max_concurrent_runs, {
      minimum: 1,
      maximum: 16,
    }),
    request_timeout_seconds: projectSafeInteger(record.request_timeout_seconds, {
      minimum: 30,
      maximum: 1_800,
    }),
    retry_limit: projectSafeInteger(record.retry_limit, { minimum: 0, maximum: 5 }),
    quarantine_error_threshold: projectSafeInteger(record.quarantine_error_threshold, {
      minimum: 0,
      maximum: 100,
    }),
    history_retention_days: projectSafeInteger(record.history_retention_days, {
      minimum: 1,
      maximum: 365,
    }),
    overload_trigger_enabled: projectBoolean(record.overload_trigger_enabled),
    overload_keywords: keywords,
    overload_retry_limit: projectSafeInteger(record.overload_retry_limit, {
      minimum: 0,
      maximum: 5,
    }),
    overload_scan_interval_seconds: projectSafeInteger(record.overload_scan_interval_seconds, {
      minimum: 5,
      maximum: 3_600,
    }),
    overload_debounce_seconds: projectSafeInteger(record.overload_debounce_seconds, {
      minimum: 0,
      maximum: 86_400,
    }),
    notify_telegram_enabled: projectBoolean(record.notify_telegram_enabled),
    notify_email_enabled: projectBoolean(record.notify_email_enabled),
    notify_on_degraded: projectBoolean(record.notify_on_degraded),
    notify_on_recovered: projectBoolean(record.notify_on_recovered),
    notify_on_error: projectBoolean(record.notify_on_error),
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
  }
}

function projectBankModel(value: unknown): DegradationBankModelDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, bankModelFields)
  return {
    id: projectString(record.id),
    display_name: projectString(record.display_name),
    family: projectString(record.family),
    family_name: projectString(record.family_name),
  }
}

export function projectDegradationCatalog(value: unknown): DegradationCatalogDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, catalogFields)
  const models = projectArray(record.models, projectBankModel)
  const efforts = projectArray(record.efforts, (item) => projectEnum(item, degradationEfforts))
  const states = projectArray(record.states, (item) => projectEnum(item, degradationStates))
  const maxSamples = projectSafeInteger(record.max_samples, { minimum: 1, maximum: 5 })
  if (
    models.length === 0 ||
    new Set(models.map(({ id }) => id)).size !== models.length ||
    new Set(efforts).size !== efforts.length ||
    new Set(states).size !== states.length
  ) {
    invalidResponse()
  }
  return {
    models,
    efforts,
    states,
    recommended_samples: projectSafeInteger(record.recommended_samples, {
      minimum: 1,
      maximum: maxSamples,
    }),
    max_samples: maxSamples,
    calibrated_samples: projectSafeInteger(record.calibrated_samples, {
      minimum: 1,
      maximum: maxSamples,
    }),
    method: projectFreeString(record.method),
    built_at: projectFreeString(record.built_at),
  }
}

export function projectDegradationOverview(value: unknown): DegradationOverviewDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, overviewFields)
  return {
    settings: projectDegradationSettings(record.settings),
    catalog: projectDegradationCatalog(record.catalog),
  }
}

function projectDegradationSummary(value: unknown): DegradationSummaryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, summaryFields)
  const summary: DegradationSummaryDto = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    enabled: projectSafeInteger(record.enabled, { minimum: 0 }),
    healthy: projectSafeInteger(record.healthy, { minimum: 0 }),
    degraded: projectSafeInteger(record.degraded, { minimum: 0 }),
    inconclusive: projectSafeInteger(record.inconclusive, { minimum: 0 }),
    error: projectSafeInteger(record.error, { minimum: 0 }),
    quota_exhausted: projectSafeInteger(record.quota_exhausted, { minimum: 0 }),
    unknown: projectSafeInteger(record.unknown, { minimum: 0 }),
  }
  const states =
    summary.healthy +
    summary.degraded +
    summary.inconclusive +
    summary.error +
    summary.quota_exhausted +
    summary.unknown
  if (states !== summary.total || summary.enabled > summary.total) invalidResponse()
  return summary
}

export function projectDegradationMonitor(value: unknown): DegradationMonitorDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, monitorFields)
  const result: DegradationMonitorDto = {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectFreeString(record.group_name),
    group_enabled: projectBoolean(record.group_enabled),
    channel_id: projectFreeString(record.channel_id),
    connection_type: projectFreeString(record.connection_type),
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    credential_mask: projectString(record.credential_mask),
    credential_status: projectFreeString(record.credential_status),
    plan_name: projectFreeString(record.plan_name),
    plan_level: projectFreeString(record.plan_level),
    upstream_model: projectString(record.upstream_model),
    expected_model: projectString(record.expected_model),
    expected_model_name: projectString(record.expected_model_name),
    reasoning_effort: projectEnum(record.reasoning_effort, degradationEfforts),
    sample_count: projectSafeInteger(record.sample_count, { minimum: 0, maximum: 5 }),
    min_probability_micros: projectProbabilityMicros(record.min_probability_micros),
    interval_seconds: projectSafeInteger(record.interval_seconds, { minimum: 0 }),
    cooldown_interval_seconds: projectSafeInteger(record.cooldown_interval_seconds, {
      minimum: 0,
    }),
    run_when_disabled: record.run_when_disabled === null
      ? null
      : projectBoolean(record.run_when_disabled),
    enabled: projectBoolean(record.enabled),
    state: projectEnum(record.state, degradationStates),
    state_reason: projectFreeString(record.state_reason),
    state_since_ms: projectEpochMilliseconds(record.state_since_ms),
    last_run_at_ms: projectEpochMilliseconds(record.last_run_at_ms),
    last_success_at_ms: projectEpochMilliseconds(record.last_success_at_ms),
    next_run_at_ms: projectEpochMilliseconds(record.next_run_at_ms),
    last_probability_micros: projectProbabilityMicros(record.last_probability_micros),
    last_detected_model: projectFreeString(record.last_detected_model),
    last_detected_model_name: projectFreeString(record.last_detected_model_name),
    last_error_code: projectFreeString(record.last_error_code),
    consecutive_errors: projectSafeInteger(record.consecutive_errors, { minimum: 0 }),
    note: projectFreeString(record.note),
    effective_sample_count: projectSafeInteger(record.effective_sample_count, {
      minimum: 1,
      maximum: 5,
    }),
    effective_min_probability_micros: projectSafeInteger(
      record.effective_min_probability_micros,
      { minimum: 1, maximum: probabilityMicrosScale },
    ),
    effective_interval_seconds: projectSafeInteger(record.effective_interval_seconds, {
      minimum: degradationMinIntervalSeconds,
      maximum: degradationMaxIntervalSeconds,
    }),
    effective_cooldown_interval_seconds: projectSafeInteger(
      record.effective_cooldown_interval_seconds,
      { minimum: degradationMinIntervalSeconds, maximum: degradationMaxIntervalSeconds },
    ),
    effective_run_when_disabled: projectBoolean(record.effective_run_when_disabled),
    created_at_ms: projectEpochMilliseconds(record.created_at_ms),
    updated_at_ms: projectEpochMilliseconds(record.updated_at_ms),
  }
  // 覆盖值为 0 表示继承全局，这时生效值必须来自全局；非 0 时两者必须一致。
  if (
    (result.sample_count !== 0 && result.sample_count !== result.effective_sample_count) ||
    (result.min_probability_micros !== 0 &&
      result.min_probability_micros !== result.effective_min_probability_micros) ||
    (result.interval_seconds !== 0 &&
      result.interval_seconds !== result.effective_interval_seconds) ||
    (result.cooldown_interval_seconds !== 0 &&
      result.cooldown_interval_seconds !== result.effective_cooldown_interval_seconds) ||
    (result.run_when_disabled !== null &&
      result.run_when_disabled !== result.effective_run_when_disabled)
  ) {
    invalidResponse()
  }
  return result
}

function projectDegradationPagination(value: unknown): DegradationPaginationDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, paginationFields)
  const pageSize = projectSafeInteger(record.page_size, { minimum: 1, maximum: 100 })
  if (pageSize !== 20 && pageSize !== 50 && pageSize !== 100) invalidResponse()
  return {
    page: projectSafeInteger(record.page, { minimum: 1 }),
    page_size: pageSize,
    total_items: projectSafeInteger(record.total_items, { minimum: 0 }),
    total_pages: projectSafeInteger(record.total_pages, { minimum: 0 }),
  }
}

export function projectDegradationMonitorCollection(
  value: unknown,
): DegradationMonitorCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, collectionFields)
  const items = projectArray(record.items, projectDegradationMonitor)
  const pagination = projectDegradationPagination(record.pagination)
  const expectedTotalPages =
    pagination.total_items === 0 ? 0 : Math.ceil(pagination.total_items / pagination.page_size)
  if (
    pagination.total_pages !== expectedTotalPages ||
    items.length > pagination.page_size ||
    new Set(items.map(({ id }) => id)).size !== items.length
  ) {
    invalidResponse()
  }
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    settings: projectDegradationSettings(record.settings),
    summary: projectDegradationSummary(record.summary),
    items,
    pagination,
    active_runs: projectSafeInteger(record.active_runs, { minimum: 0 }),
  }
}

function projectRejectedTarget(value: unknown): DegradationRejectedTargetDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, rejectedFields)
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 0 }),
    credential_id: projectSafeInteger(record.credential_id, { minimum: 0 }),
    reason: projectString(record.reason),
  }
}

export function projectDegradationEnrollResult(value: unknown): DegradationEnrollResultDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, enrollResultFields)
  return {
    created: projectSafeInteger(record.created, { minimum: 0 }),
    updated: projectSafeInteger(record.updated, { minimum: 0 }),
    skipped: projectSafeInteger(record.skipped, { minimum: 0 }),
    rejected: projectArray(record.rejected, projectRejectedTarget),
    items: projectArray(record.items, projectDegradationMonitor),
  }
}

export function projectDegradationBatchResult(value: unknown): DegradationBatchResultDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, batchResultFields)
  return {
    action: projectEnum(record.action, degradationBatchActions),
    affected: projectSafeInteger(record.affected, { minimum: 0 }),
  }
}

function projectRankingEntry(value: unknown): DegradationRankingEntryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, rankingFields)
  return {
    model: projectString(record.model),
    display_name: projectFreeString(record.display_name),
    family: projectFreeString(record.family),
    probability_micros: projectProbabilityMicros(record.probability_micros),
  }
}

function projectSampleDiagnostic(value: unknown): DegradationSampleDiagnosticDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, diagnosticFields)
  return {
    index: projectSafeInteger(record.index, { minimum: 0 }),
    parsed_numbers: projectSafeInteger(record.parsed_numbers, { minimum: 0 }),
    minimum_numbers: projectSafeInteger(record.minimum_numbers, { minimum: 0 }),
    accepted: projectBoolean(record.accepted),
  }
}

function projectSampleText(value: unknown): DegradationSampleTextDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, sampleTextFields)
  return {
    index: projectSafeInteger(record.index, { minimum: 0 }),
    expected_count: projectSafeInteger(record.expected_count, { minimum: 0 }),
    text: projectFreeString(record.text),
    truncated: projectBoolean(record.truncated),
  }
}

function projectDegradationRunStats(value: unknown): DegradationRunStatsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, runStatsFields)
  const stats: DegradationRunStatsDto = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    healthy: projectSafeInteger(record.healthy, { minimum: 0 }),
    degraded: projectSafeInteger(record.degraded, { minimum: 0 }),
    inconclusive: projectSafeInteger(record.inconclusive, { minimum: 0 }),
    error: projectSafeInteger(record.error, { minimum: 0 }),
    quota_exhausted: projectSafeInteger(record.quota_exhausted, { minimum: 0 }),
    unknown: projectSafeInteger(record.unknown, { minimum: 0 }),
    schedule: projectSafeInteger(record.schedule, { minimum: 0 }),
    manual: projectSafeInteger(record.manual, { minimum: 0 }),
    overload: projectSafeInteger(record.overload, { minimum: 0 }),
  }
  const outcomes =
    stats.healthy +
    stats.degraded +
    stats.inconclusive +
    stats.error +
    stats.quota_exhausted +
    stats.unknown
  if (outcomes !== stats.total || stats.schedule + stats.manual + stats.overload > stats.total) {
    invalidResponse()
  }
  return stats
}

export function projectDegradationRun(value: unknown): DegradationRunDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, runFields)
  const startedAtMS = projectEpochMilliseconds(record.started_at_ms)
  const completedAtMS = projectEpochMilliseconds(record.completed_at_ms)
  if (completedAtMS < startedAtMS) invalidResponse()
  const sampleCount = projectSafeInteger(record.sample_count, { minimum: 0, maximum: 5 })
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    monitor_id: projectSafeInteger(record.monitor_id, { minimum: 1 }),
    trigger: projectEnum(record.trigger, degradationTriggers),
    outcome: projectEnum(record.outcome, degradationStates),
    started_at_ms: startedAtMS,
    completed_at_ms: completedAtMS,
    duration_ms: projectSafeInteger(record.duration_ms, { minimum: 0 }),
    expected_model: projectFreeString(record.expected_model),
    detected_model: projectFreeString(record.detected_model),
    detected_model_name: projectFreeString(record.detected_model_name),
    expected_probability_micros: projectProbabilityMicros(record.expected_probability_micros),
    leading_probability_micros: projectProbabilityMicros(record.leading_probability_micros),
    min_probability_micros: projectProbabilityMicros(record.min_probability_micros),
    sample_count: sampleCount,
    used_samples: projectSafeInteger(record.used_samples, { minimum: 0, maximum: sampleCount }),
    attempts: projectSafeInteger(record.attempts, { minimum: 0 }),
    reasons: projectArray(record.reasons, projectString),
    error_code: projectFreeString(record.error_code),
    error_summary: projectFreeString(record.error_summary),
    ranking: projectArray(record.ranking, projectRankingEntry),
    diagnostics: projectArray(record.diagnostics, projectSampleDiagnostic),
    samples: projectArray(record.samples, projectSampleText),
  }
}

export function projectDegradationRunCollection(value: unknown): DegradationRunCollectionDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, runCollectionFields)
  const items = projectArray(record.items, projectDegradationRun)
  const stats = projectDegradationRunStats(record.stats)
  if (
    items.length > degradationRunHistoryLimit ||
    items.length > stats.total ||
    new Set(items.map(({ id }) => id)).size !== items.length
  ) {
    invalidResponse()
  }
  return { monitor_id: projectSafeInteger(record.monitor_id, { minimum: 1 }), items, stats }
}

export async function getDegradationOverview(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<DegradationOverviewDto> {
  return projectDegradationOverview(
    await client.request('/api/degradation/overview', { method: 'GET', signal }),
  )
}

export async function updateDegradationSettings(
  client: ApiClient,
  request: DegradationSettingsUpdateRequest,
  signal?: AbortSignal,
): Promise<DegradationSettingsDto> {
  const payload: Record<string, unknown> = {}
  for (const field of settingsUpdateFields) {
    const value = request[field]
    if (value !== undefined) payload[field] = value
  }
  return projectDegradationSettings(
    await client.request('/api/degradation/settings', { method: 'PUT', json: payload, signal }),
  )
}

export async function listDegradationMonitors(
  client: ApiClient,
  filters: DegradationMonitorFilters,
  signal?: AbortSignal,
): Promise<DegradationMonitorCollectionDto> {
  const normalized = normalizeDegradationMonitorFilters(filters)
  const params = new URLSearchParams({
    page: String(normalized.page),
    page_size: String(normalized.page_size),
  })
  if (normalized.query) params.set('query', normalized.query)
  if (normalized.state) params.set('state', normalized.state)
  if (normalized.group_id !== undefined) params.set('group_id', String(normalized.group_id))
  if (normalized.enabled !== undefined) params.set('enabled', String(normalized.enabled))
  const result = projectDegradationMonitorCollection(
    await client.request(`/api/degradation/monitors?${params.toString()}`, {
      method: 'GET',
      signal,
    }),
  )
  if (
    result.pagination.page !== normalized.page ||
    result.pagination.page_size !== normalized.page_size
  ) {
    invalidResponse()
  }
  return result
}

export async function enrollDegradationMonitors(
  client: ApiClient,
  request: DegradationEnrollRequest,
  signal?: AbortSignal,
): Promise<DegradationEnrollResultDto> {
  return projectDegradationEnrollResult(
    await client.request('/api/degradation/monitors', {
      method: 'POST',
      json: Object.fromEntries(enrollFields.map((field) => [field, request[field]])),
      signal,
    }),
  )
}

export async function updateDegradationMonitor(
  client: ApiClient,
  monitorID: number,
  request: DegradationMonitorUpdateRequest,
  signal?: AbortSignal,
): Promise<DegradationMonitorDto> {
  return projectDegradationMonitor(
    await client.request(`/api/degradation/monitors/${monitorID}`, {
      method: 'PUT',
      json: request,
      signal,
    }),
  )
}

export function deleteDegradationMonitor(
  client: ApiClient,
  monitorID: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>(`/api/degradation/monitors/${monitorID}`, {
    method: 'DELETE',
    signal,
  })
}

export async function batchDegradationMonitors(
  client: ApiClient,
  request: DegradationBatchRequest,
  signal?: AbortSignal,
): Promise<DegradationBatchResultDto> {
  const result = projectDegradationBatchResult(
    await client.request('/api/degradation/monitors/batch', {
      method: 'POST',
      json: { action: request.action, monitor_ids: request.monitor_ids },
      signal,
    }),
  )
  if (result.action !== request.action) invalidResponse()
  return result
}

export async function runDegradationMonitor(
  client: ApiClient,
  monitorID: number,
  signal?: AbortSignal,
): Promise<DegradationRunDto> {
  const result = projectDegradationRun(
    await client.request(`/api/degradation/monitors/${monitorID}/run`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
  if (result.monitor_id !== monitorID) invalidResponse()
  return result
}

export async function listDegradationRuns(
  client: ApiClient,
  monitorID: number,
  signal?: AbortSignal,
): Promise<DegradationRunCollectionDto> {
  const result = projectDegradationRunCollection(
    await client.request(`/api/degradation/monitors/${monitorID}/runs`, {
      method: 'GET',
      signal,
    }),
  )
  if (result.monitor_id !== monitorID) invalidResponse()
  return result
}

const manualDegradationQueryOptions = {
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

export function degradationOverviewQueryOptions(client: ApiClient) {
  return queryOptions({
    ...manualDegradationQueryOptions,
    queryKey: controlQueryKeys.degradation.overview(),
    queryFn: ({ signal }) => getDegradationOverview(client, signal),
    // 检测目录与全局配置可能被其它标签页改动，进入监控页时必须重新校验。
    refetchOnMount: 'always',
  })
}

export function degradationMonitorCollectionQueryOptions(
  client: ApiClient,
  filters: MaybeRefOrGetter<DegradationMonitorFilters>,
) {
  return queryOptions({
    ...manualDegradationQueryOptions,
    queryKey: computed(() =>
      controlQueryKeys.degradation.monitors(toValue(filters)),
    ),
    queryFn: ({ queryKey, signal }) => listDegradationMonitors(client, queryKey[3], signal),
    placeholderData: keepPreviousData,
    // 批量立即检测由后台按并发上限消化，只要还有排队或在跑的检测就继续轮询，
    // 全部跑完自动停下，空闲的页面不会多发一个请求。
    refetchInterval: (query) =>
      (query.state.data?.active_runs ?? 0) > 0 ? degradationActivePollIntervalMs : false,
    refetchIntervalInBackground: false,
  })
}

export function degradationRunsQueryOptions(
  client: ApiClient,
  monitorID: MaybeRefOrGetter<number | undefined>,
) {
  return queryOptions({
    ...manualDegradationQueryOptions,
    queryKey: computed(() => controlQueryKeys.degradation.runs(toValue(monitorID) ?? 0)),
    queryFn: ({ queryKey, signal }) => listDegradationRuns(client, queryKey[3], signal),
    enabled: computed(() => (toValue(monitorID) ?? 0) > 0),
  })
}
