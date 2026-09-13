<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Plus, RefreshCw, Search, SlidersHorizontal, TrendingDown } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { useCollectionLoading } from '@/app/loading-state'
import { controlQueryKeys } from '@/app/query-keys'
import {
  batchDegradationMonitors,
  degradationMonitorCollectionQueryOptions,
  degradationOverviewQueryOptions,
  deleteDegradationMonitor,
  runDegradationMonitor,
  type DegradationBatchActionValue,
  type DegradationMonitorDto,
  type DegradationMonitorFilters,
} from '@/app/resources/degradation'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import { useToast } from '@/app/toast'
import { useDebouncedAction } from '@/app/use-debounced-action'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

import DegradationBatchBar from './DegradationBatchBar.vue'
import DegradationEnrollDrawer from './DegradationEnrollDrawer.vue'
import DegradationMonitorDrawer from './DegradationMonitorDrawer.vue'
import DegradationMonitorRecord from './DegradationMonitorRecord.vue'
import DegradationRunsDrawer from './DegradationRunsDrawer.vue'
import DegradationSettingsDrawer from './DegradationSettingsDrawer.vue'
import MonitorSectionHeading from './MonitorSectionHeading.vue'
import {
  degradationStateOrder,
  degradationStateTone,
  isDegradationState,
} from './degradation-presenter'
import { useDegradationLabels } from './use-degradation-labels'

interface DeleteTarget {
  ids: number[]
  target?: string
}

const client = useApiClient()
const queryClient = useQueryClient()
const toast = useToast()
const { n, t } = useI18n()
const { durationLabel, probabilityLabel, stateLabel } = useDegradationLabels()

const filters = ref<DegradationMonitorFilters>({ page: 1, page_size: 20 })
const searchDraft = ref('')
const searchDebounce = useDebouncedAction(250)
const selectedIds = ref(new Set<number>())
const pendingOperations = ref(new Set<string>())
const feedback = ref('')
const settingsOpen = ref(false)
const enrollOpen = ref(false)
const editTarget = ref<DegradationMonitorDto>()
const runsTarget = ref<DegradationMonitorDto>()
const deleteTarget = ref<DeleteTarget>()

const overviewQuery = useQuery(degradationOverviewQueryOptions(client))
const collectionQuery = useQuery(degradationMonitorCollectionQueryOptions(client, filters))
const groupsQuery = useQuery(groupOptionsQueryOptions(client))

const collection = computed(() => collectionQuery.data.value)
const overview = computed(() => overviewQuery.data.value)
// 列表响应带着当次读取的全局配置，比 overview 更贴近当前继承值。
const settings = computed(() => collection.value?.settings ?? overview.value?.settings)
const catalog = computed(() => overview.value?.catalog)
const summary = computed(() => collection.value?.summary)
const items = computed(() => collection.value?.items ?? [])

const collectionLoading = useCollectionLoading({
  pending: () => collectionQuery.isPending.value || overviewQuery.isPending.value,
  placeholder: () => collectionQuery.isPlaceholderData.value,
  fetching: () => collectionQuery.isFetching.value || overviewQuery.isFetching.value,
  hasData: () => collection.value !== undefined,
  itemCount: () => items.value.length,
})
const initialLoading = collectionLoading.initial
const collectionTransition = collectionLoading.transition
const collectionRefreshing = collectionLoading.refreshing
const skeletonRows = collectionLoading.rows

const batchBusy = computed(() =>
  [...pendingOperations.value].some((key) => key.startsWith('batch:')),
)
const deleteBusy = computed(() => pendingOperations.value.has('batch:delete'))
const selectedCount = computed(() => selectedIds.value.size)
const allVisibleSelected = computed(
  () => items.value.length > 0 && items.value.every((item) => selectedIds.value.has(item.id)),
)
const hasChangedConditions = computed(
  () =>
    Boolean(filters.value.query) ||
    filters.value.state !== undefined ||
    filters.value.group_id !== undefined ||
    filters.value.enabled !== undefined,
)
const configMeta = computed(() => {
  const current = settings.value
  if (!current) return undefined
  return [
    t(current.enabled ? 'monitor.degradation.meta.on' : 'monitor.degradation.meta.off'),
    t('monitor.degradation.meta.interval', { value: durationLabel(current.interval_seconds) }),
    t('monitor.degradation.meta.cooldown', {
      value: durationLabel(current.cooldown_interval_seconds),
    }),
    t('monitor.degradation.meta.samples', { count: n(current.sample_count) }),
    t('monitor.degradation.meta.threshold', {
      value: probabilityLabel(current.min_probability_micros),
    }),
  ].join(' · ')
})
// 首项 value 为 undefined，CollectionStatusSummary 用它渲染“取消状态筛选”的按钮。
const statusSummaryItems = computed(() => {
  const current = summary.value
  if (!current) return []
  return [
    {
      value: undefined,
      label: t('monitor.degradation.filters.allStates'),
      count: current.total,
      tone: 'neutral' as const,
    },
    ...degradationStateOrder.map((state) => ({
      value: state,
      label: stateLabel(state),
      count: current[state],
      tone: degradationStateTone(state),
    })),
  ]
})
const groupSelectOptions = computed(() => [
  { value: '', label: t('monitor.degradation.filters.allGroups') },
  ...(groupsQuery.data.value ?? []).map((group) => ({
    value: String(group.id),
    label: group.name,
  })),
])
const enabledSelectOptions = computed(() => [
  { value: '', label: t('monitor.degradation.filters.anySchedule') },
  { value: 'enabled', label: t('monitor.degradation.filters.onlyEnabled') },
  { value: 'paused', label: t('monitor.degradation.filters.onlyPaused') },
])
const enabledFilterValue = computed(() =>
  filters.value.enabled === undefined ? '' : filters.value.enabled ? 'enabled' : 'paused',
)

// 换页或换条件后原来的勾选多半已经不在视野里，留着会让批量操作打到看不见的行上。
watch(
  () => [
    filters.value.query,
    filters.value.state,
    filters.value.group_id,
    filters.value.enabled,
    filters.value.page,
    filters.value.page_size,
  ],
  () => {
    selectedIds.value = new Set()
  },
)
// 删光最后一页会停在空页上，只剩筛选空态可看，得退回到真实的最后一页。
watch(
  () => ({
    totalPages: collection.value?.pagination.total_pages,
    page: filters.value.page,
    placeholder: collectionQuery.isPlaceholderData.value,
  }),
  ({ totalPages, page, placeholder }) => {
    if (!placeholder && totalPages !== undefined && totalPages > 0 && page > totalPages) {
      filters.value = { ...filters.value, page: totalPages }
    }
  },
)

function setPending(key: string, active: boolean): void {
  const next = new Set(pendingOperations.value)
  if (active) next.add(key)
  else next.delete(key)
  pendingOperations.value = next
}

function rowBusy(id: number): boolean {
  return pendingOperations.value.has(`monitor:${id}`) || batchBusy.value
}

function patchFilters(patch: Partial<DegradationMonitorFilters>): void {
  filters.value = { ...filters.value, ...patch, page: 1 }
}

function scheduleSearch(value: string): void {
  searchDebounce.schedule(() => {
    const query = value.trim()
    patchFilters({ query: query === '' ? undefined : query })
  })
}

function clearSearch(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  patchFilters({ query: undefined })
}

function setState(value: string | undefined): void {
  patchFilters({ state: isDegradationState(value) ? value : undefined })
}

function setGroup(value: string): void {
  patchFilters({ group_id: value === '' ? undefined : Number(value) })
}

function setEnabled(value: string): void {
  patchFilters({ enabled: value === '' ? undefined : value === 'enabled' })
}

function resetFilters(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  filters.value = { page: 1, page_size: filters.value.page_size }
}

function setPage(page: number): void {
  filters.value = { ...filters.value, page: Math.max(1, page) }
}

function setPageSize(pageSize: 20 | 50 | 100): void {
  filters.value = { ...filters.value, page: 1, page_size: pageSize }
}

function setSelected(id: number, selected: boolean): void {
  const next = new Set(selectedIds.value)
  if (selected) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}

function toggleAllVisible(): void {
  selectedIds.value = allVisibleSelected.value
    ? new Set()
    : new Set(items.value.map((item) => item.id))
}

function monitorLabel(monitor: DegradationMonitorDto): string {
  return monitor.credential_id > 0 ? monitor.credential_mask : monitor.group_name
}

async function invalidateMonitors(): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: controlQueryKeys.degradation.monitorsAll })
}

async function refreshAll(): Promise<void> {
  feedback.value = ''
  await Promise.all([overviewQuery.refetch(), collectionQuery.refetch()])
}

async function runNow(monitor: DegradationMonitorDto): Promise<void> {
  const key = `monitor:${monitor.id}`
  if (pendingOperations.value.has(key) || batchBusy.value) return
  setPending(key, true)
  feedback.value = ''
  try {
    const run = await runDegradationMonitor(client, monitor.id)
    toast.show({
      message: t('monitor.degradation.toast.runFinished', {
        target: monitorLabel(monitor),
        state: stateLabel(run.outcome),
      }),
      tone: run.outcome === 'healthy' ? 'success' : 'warning',
    })
    await Promise.all([
      invalidateMonitors(),
      queryClient.invalidateQueries({
        queryKey: controlQueryKeys.degradation.runs(monitor.id),
      }),
    ])
  } catch {
    feedback.value = t('monitor.degradation.runFailed', { target: monitorLabel(monitor) })
  } finally {
    setPending(key, false)
  }
}

// ids 显式传入，删除确认弹窗才不会依赖“勾选集合此刻仍等于弹窗里的那批”。
async function runBatch(
  action: DegradationBatchActionValue,
  ids: number[] = [...selectedIds.value],
): Promise<boolean> {
  const key = `batch:${action}`
  if (ids.length === 0 || batchBusy.value) return false
  setPending(key, true)
  feedback.value = ''
  try {
    const result = await batchDegradationMonitors(client, { action, monitor_ids: ids })
    toast.show({
      message: t(`monitor.degradation.toast.batch.${action}`, { count: n(result.affected) }),
      tone: 'success',
    })
    if (action === 'delete') selectedIds.value = new Set()
    await invalidateMonitors()
    return true
  } catch {
    feedback.value = t('monitor.degradation.batchFailed')
    return false
  } finally {
    setPending(key, false)
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || batchBusy.value) return
  if (target.ids.length !== 1) {
    if (await runBatch('delete', target.ids)) deleteTarget.value = undefined
    return
  }
  const id = target.ids[0]
  setPending('batch:delete', true)
  feedback.value = ''
  try {
    await deleteDegradationMonitor(client, id)
    selectedIds.value = new Set([...selectedIds.value].filter((value) => value !== id))
    toast.show({ message: t('monitor.degradation.toast.deleted'), tone: 'success' })
    await invalidateMonitors()
    deleteTarget.value = undefined
  } catch {
    feedback.value = t('monitor.degradation.deleteFailed')
  } finally {
    setPending('batch:delete', false)
  }
}

async function onEnrolled(): Promise<void> {
  await invalidateMonitors()
}

async function onMonitorSaved(): Promise<void> {
  editTarget.value = undefined
  await invalidateMonitors()
}

async function onSettingsSaved(): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: controlQueryKeys.degradation.overview() }),
    invalidateMonitors(),
  ])
}
</script>

<template>
  <section
    class="degradation-tab"
    aria-labelledby="degradation-title"
    :aria-busy="collectionQuery.isFetching.value || batchBusy ? 'true' : undefined"
  >
    <MonitorSectionHeading
      id="degradation-title"
      :title="t('monitor.degradation.title')"
      :description="t('monitor.degradation.description')"
      :meta="configMeta"
    >
      <template #actions>
        <AppButton
          variant="secondary"
          size="compact"
          :disabled="!settings || !catalog"
          @click="settingsOpen = true"
        >
          <SlidersHorizontal :size="15" aria-hidden="true" />
          {{ t('monitor.degradation.configure') }}
        </AppButton>
        <AppButton size="compact" :disabled="!settings || !catalog" @click="enrollOpen = true">
          <Plus :size="15" aria-hidden="true" />
          {{ t('monitor.degradation.add') }}
        </AppButton>
        <IconButton
          variant="ghost"
          size="compact"
          :busy="collectionQuery.isFetching.value"
          :label="t('monitor.degradation.refresh')"
          @click="refreshAll"
        >
          <RefreshCw :size="15" aria-hidden="true" />
        </IconButton>
      </template>
    </MonitorSectionHeading>

    <InlineFeedback v-if="settings && !settings.enabled" tone="warning" appearance="ledger">
      {{ t('monitor.degradation.globallyDisabled') }}
    </InlineFeedback>

    <AsyncRefreshIndicator
      :active="collectionRefreshing"
      :label="t('monitor.degradation.loading')"
    />

    <SkeletonSurface
      v-if="collectionQuery.isPending.value || overviewQuery.isPending.value || initialLoading"
      variant="collection"
      :rows="filters.page_size"
      :columns="6"
      row-height="56px"
      mobile-row-height="180px"
      show-controls
      :concealed="!initialLoading"
      :label="t('monitor.degradation.loading')"
    />
    <QueryFeedback
      v-else-if="
        (collectionQuery.isError.value && !collection) || (overviewQuery.isError.value && !catalog)
      "
      state="error"
      :message="t('monitor.degradation.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="refreshAll"
    />
    <template v-else-if="collection && summary && settings && catalog">
      <QueryFeedback
        v-if="collectionQuery.isError.value || overviewQuery.isError.value"
        state="stale"
        :message="t('monitor.degradation.stale')"
        :retry-label="t('common.retry')"
        @retry="refreshAll"
      />
      <p v-if="feedback" class="degradation-tab__feedback" role="alert">{{ feedback }}</p>

      <CollectionStatusSummary
        v-if="summary.total > 0"
        :total="summary.total"
        :items="statusSummaryItems"
        :model-value="filters.state"
        :label="t('monitor.degradation.summary.region')"
        :total-label="t('monitor.degradation.summary.current')"
        appearance="detail"
        @update:model-value="setState"
      />

      <div v-if="summary.total > 0" class="degradation-tab__tools">
        <div class="degradation-tab__search-field">
          <AppSearchInput
            v-model="searchDraft"
            class="degradation-tab__search-input"
            :label="t('monitor.degradation.filters.search')"
            :placeholder="t('monitor.degradation.filters.searchPlaceholder')"
            :clear-label="t('monitor.degradation.filters.clearSearch')"
            @update:model-value="scheduleSearch"
            @clear="clearSearch"
          />
          <AppButton
            variant="link"
            size="inline"
            :class="{ 'degradation-tab__reset-placeholder': !hasChangedConditions }"
            :aria-hidden="!hasChangedConditions"
            :tabindex="hasChangedConditions ? undefined : -1"
            :disabled="!hasChangedConditions"
            @click="resetFilters"
          >
            {{ t('monitor.degradation.filters.reset') }}
          </AppButton>
        </div>
        <AppSelect
          class="degradation-tab__filter-select"
          :model-value="filters.group_id === undefined ? '' : String(filters.group_id)"
          :label="t('monitor.degradation.filters.group')"
          :options="groupSelectOptions"
          size="compact"
          @update:model-value="setGroup"
        />
        <AppSelect
          class="degradation-tab__filter-select"
          :model-value="enabledFilterValue"
          :label="t('monitor.degradation.filters.enabled')"
          :options="enabledSelectOptions"
          size="compact"
          @update:model-value="setEnabled"
        />
        <DegradationBatchBar
          class="degradation-tab__batch-bar"
          :selected-count="selectedCount"
          :all-visible-selected="allVisibleSelected"
          :can-select-all="items.length > 0"
          :pending="batchBusy"
          @toggle-select="toggleAllVisible"
          @enable="runBatch('enable')"
          @disable="runBatch('disable')"
          @clear="runBatch('clear')"
          @remove="deleteTarget = { ids: [...selectedIds] }"
        />
      </div>

      <SkeletonSurface
        v-if="collectionTransition"
        variant="collection"
        :rows="skeletonRows"
        :columns="6"
        row-height="56px"
        mobile-row-height="180px"
        :label="t('monitor.degradation.loading')"
      />
      <EmptyState
        v-else-if="summary.total === 0"
        class="degradation-tab__empty"
        variant="ledger"
        heading-as="h3"
        :title="t('monitor.degradation.empty.title')"
        :description="t('monitor.degradation.empty.description')"
      >
        <template #icon><TrendingDown :size="22" stroke-width="1.7" /></template>
        <template #actions>
          <AppButton size="compact" @click="enrollOpen = true">
            <Plus :size="15" aria-hidden="true" />
            {{ t('monitor.degradation.add') }}
          </AppButton>
        </template>
      </EmptyState>
      <EmptyState
        v-else-if="collection.pagination.total_items === 0"
        variant="ledger"
        heading-as="h3"
        :title="t('monitor.degradation.emptyFilterTitle')"
        :description="t('monitor.degradation.emptyFilterDescription')"
      >
        <template #icon><Search :size="20" aria-hidden="true" /></template>
        <template #actions>
          <AppButton variant="secondary" size="compact" @click="resetFilters">
            {{ t('monitor.degradation.filters.reset') }}
          </AppButton>
        </template>
      </EmptyState>
      <template v-else>
        <LedgerRecordList
          :label="t('monitor.degradation.tableLabel')"
          :row-count="collection.pagination.total_items + 1"
          :scroll-hint="t('monitor.scrollHint')"
          grid-class="degradation-grid"
        >
          <template #header>
            <span class="degradation-tab__select-heading" role="columnheader">
              <span class="sr-only">{{ t('monitor.degradation.columns.select') }}</span>
            </span>
            <span role="columnheader">{{ t('monitor.degradation.columns.target') }}</span>
            <span role="columnheader">{{ t('monitor.degradation.columns.probe') }}</span>
            <span role="columnheader">{{ t('monitor.degradation.columns.state') }}</span>
            <span role="columnheader">{{ t('monitor.degradation.columns.probability') }}</span>
            <span role="columnheader">{{ t('monitor.degradation.columns.schedule') }}</span>
            <span class="degradation-tab__actions-heading" role="columnheader">
              {{ t('monitor.degradation.columns.actions') }}
            </span>
          </template>

          <DegradationMonitorRecord
            v-for="(item, index) in items"
            :key="item.id"
            :item="item"
            :row-index="
              (collection.pagination.page - 1) * collection.pagination.page_size + index + 2
            "
            :selected="selectedIds.has(item.id)"
            :busy="rowBusy(item.id)"
            @update:selected="setSelected(item.id, $event)"
            @run="runNow"
            @edit="editTarget = $event"
            @history="runsTarget = $event"
            @remove="deleteTarget = { ids: [$event.id], target: monitorLabel($event) }"
          />
        </LedgerRecordList>
        <PaginationBar
          :page="collection.pagination.page"
          :page-size="collection.pagination.page_size"
          :total-items="collection.pagination.total_items"
          :total-pages="collection.pagination.total_pages"
          show-page-size
          appearance="detail"
          :pending="collectionQuery.isFetching.value || batchBusy"
          @previous="setPage(filters.page - 1)"
          @next="setPage(filters.page + 1)"
          @update:page-size="setPageSize"
        />
      </template>
    </template>

    <DegradationSettingsDrawer
      v-if="settings && catalog"
      v-model:open="settingsOpen"
      :settings="settings"
      :catalog="catalog"
      @saved="onSettingsSaved"
    />
    <DegradationEnrollDrawer
      v-if="settings && catalog"
      v-model:open="enrollOpen"
      :settings="settings"
      :catalog="catalog"
      @enrolled="onEnrolled"
    />
    <DegradationMonitorDrawer
      v-if="settings && catalog"
      :open="editTarget !== undefined"
      :monitor="editTarget"
      :settings="settings"
      :catalog="catalog"
      @update:open="!$event && (editTarget = undefined)"
      @saved="onMonitorSaved"
    />
    <DegradationRunsDrawer
      :open="runsTarget !== undefined"
      :monitor="runsTarget"
      @update:open="!$event && (runsTarget = undefined)"
    />
    <AppConfirmDialog
      appearance="ledger"
      tone="danger"
      :open="deleteTarget !== undefined"
      :title="
        deleteTarget?.ids.length === 1
          ? t('monitor.degradation.deleteTitle')
          : t('monitor.degradation.batch.deleteTitle')
      "
      :description="
        deleteTarget?.ids.length === 1
          ? t('monitor.degradation.deleteDescription', { target: deleteTarget.target ?? '' })
          : t('monitor.degradation.batch.deleteDescription', {
              count: n(deleteTarget?.ids.length ?? 0),
            })
      "
      :close-label="t('common.close')"
      :cancel-label="t('common.cancel')"
      :confirm-label="t('monitor.degradation.confirmDelete')"
      :pending="deleteBusy"
      @update:open="!$event && !deleteBusy && (deleteTarget = undefined)"
      @confirm="confirmDelete"
    />
  </section>
</template>

<style scoped>
.degradation-tab {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

.degradation-tab__feedback {
  margin: 0;
  border: 1px solid var(--color-feedback-danger-border);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-text);
  padding: var(--space-3);
  line-height: var(--line-normal);
  overflow-wrap: anywhere;
}

.degradation-tab__tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: var(--space-3);
}

.degradation-tab__search-field {
  display: flex;
  min-width: 0;
  align-items: center;
  flex: 1 1 240px;
  max-width: 400px;
  gap: 10px;
}

.degradation-tab__search-input {
  min-width: 0;
  flex: 1 1 auto;
}

.degradation-tab__reset-placeholder {
  visibility: hidden;
  pointer-events: none;
}

/* AppSelect 把 class 透传到触发器本身，宽度直接写在这里即可。 */
.degradation-tab__filter-select {
  flex: 0 1 168px;
}

.degradation-tab__batch-bar {
  margin-left: auto;
}

.degradation-tab__empty {
  min-height: 330px;
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-card);
}

.degradation-grid {
  --ledger-record-list-grid: 32px minmax(160px, 1.25fr) minmax(150px, 1.15fr) minmax(118px, 0.95fr)
    minmax(104px, 0.8fr) minmax(126px, 0.95fr) 128px;
  --ledger-record-list-column-gap: 10px;
}

.degradation-tab__actions-heading {
  text-align: right;
}

@media (max-width: 860px) {
  .degradation-tab__search-field,
  .degradation-tab__filter-select {
    flex: 1 1 100%;
    max-width: none;
  }

  .degradation-tab__batch-bar {
    margin-left: 0;
  }
}
</style>
