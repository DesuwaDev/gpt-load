<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import type { CredentialCollectionFilters } from '@/api/control/types'
import { credentialCollectionQueryOptions } from '@/app/resources/credentials'
import {
  degradationMaxEnrollTargets,
  enrollDegradationMonitors,
  type DegradationCatalogDto,
  type DegradationEnrollResultDto,
  type DegradationSettingsDto,
  type DegradationTargetRequest,
} from '@/app/resources/degradation'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SearchableMultiSelect from '@/components/ui/SearchableMultiSelect.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'

import DegradationProbeFields from './DegradationProbeFields.vue'
import DegradationToggleRow from './DegradationToggleRow.vue'
import {
  applyProbeField,
  emptyProbeDraft,
  probeDraftErrors,
  probeOverrides,
  type DegradationProbeDraft,
  type DegradationProbeField,
} from './degradation-form'
import { useDegradationLabels } from './use-degradation-labels'

type EnrollMode = 'group' | 'credential'

const credentialPageSize = 100 as const

const props = defineProps<{
  open: boolean
  settings: DegradationSettingsDto
  catalog: DegradationCatalogDto
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  enrolled: [result: DegradationEnrollResultDto]
}>()
const client = useApiClient()
const { n, t } = useI18n()
const { tokenLabel } = useDegradationLabels()

const mode = ref<EnrollMode>('group')
const groupIds = ref<number[]>([])
const credentialGroupId = ref(0)
const credentialIds = ref(new Set<number>())
const credentialSearch = ref('')
const draft = ref<DegradationProbeDraft>(emptyProbeDraft())
const skipExisting = ref(true)
const showErrors = ref(false)
const submitting = ref(false)
const feedback = ref('')
const result = ref<DegradationEnrollResultDto>()

const groupsQuery = useQuery(
  groupOptionsQueryOptions(
    client,
    computed(() => props.open),
  ),
)
const credentialFilters = computed<CredentialCollectionFilters>(() => {
  const filters: CredentialCollectionFilters = { page: 1, page_size: credentialPageSize }
  const query = credentialSearch.value.trim()
  if (query) filters.q = query
  return filters
})
const credentialsQuery = useQuery({
  ...credentialCollectionQueryOptions(client, credentialGroupId, credentialFilters),
  enabled: computed(
    () => props.open && mode.value === 'credential' && credentialGroupId.value > 0,
  ),
  // 抽屉里的挑选清单不需要限额实时数，关掉轮询。
  refetchInterval: false as const,
})

watch(
  () => props.open,
  (open) => {
    if (!open) return
    mode.value = 'group'
    groupIds.value = []
    credentialGroupId.value = 0
    credentialIds.value = new Set()
    credentialSearch.value = ''
    draft.value = emptyProbeDraft()
    skipExisting.value = true
    showErrors.value = false
    feedback.value = ''
    result.value = undefined
  },
)

// 换分组后原来的凭据勾选全部失效，留着只会误提交。
watch(credentialGroupId, () => {
  credentialIds.value = new Set()
  credentialSearch.value = ''
})

const groupOptions = computed(() =>
  (groupsQuery.data.value ?? []).map((group) => ({
    value: group.id,
    label: group.name,
    description: group.enabled
      ? group.channel_id
      : `${group.channel_id} · ${t('monitor.degradation.enroll.groupDisabled')}`,
  })),
)
const groupSelectOptions = computed(() => [
  { value: '0', label: t('monitor.degradation.enroll.groupPlaceholder') },
  ...(groupsQuery.data.value ?? []).map((group) => ({
    value: String(group.id),
    label: group.name,
  })),
])
const credentialItems = computed(() => credentialsQuery.data.value?.items ?? [])
const credentialTotal = computed(() => credentialsQuery.data.value?.pagination.total_items ?? 0)
const credentialTruncated = computed(() => credentialTotal.value > credentialItems.value.length)
const allCredentialsSelected = computed(
  () =>
    credentialItems.value.length > 0 &&
    credentialItems.value.every(({ credential_id }) => credentialIds.value.has(credential_id)),
)
const selectedGroups = computed(() => {
  const options = groupsQuery.data.value ?? []
  const ids = mode.value === 'group' ? groupIds.value : [credentialGroupId.value]
  return options.filter((group) => ids.includes(group.id))
})
const modelSuggestions = computed(() => [
  ...new Set(selectedGroups.value.flatMap((group) => group.models)),
])
const targets = computed<DegradationTargetRequest[]>(() =>
  mode.value === 'group'
    ? groupIds.value.map((id) => ({ group_id: id, credential_id: 0 }))
    : [...credentialIds.value].map((id) => ({
        group_id: credentialGroupId.value,
        credential_id: id,
      })),
)
const errors = computed(() => probeDraftErrors(draft.value, props.catalog.max_samples))
const modeOptions = computed(() => [
  { value: 'group', label: t('monitor.degradation.enroll.byGroup') },
  { value: 'credential', label: t('monitor.degradation.enroll.byCredential') },
])
const targetSummary = computed(() =>
  mode.value === 'group'
    ? t('monitor.degradation.enroll.groupSummary', { count: n(groupIds.value.length) })
    : t('monitor.degradation.enroll.credentialSummary', { count: n(credentialIds.value.size) }),
)

function setMode(value: string): void {
  mode.value = value === 'credential' ? 'credential' : 'group'
}

function toggleCredential(id: number, checked: boolean): void {
  const next = new Set(credentialIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  credentialIds.value = next
}

function toggleAllCredentials(): void {
  if (allCredentialsSelected.value) {
    credentialIds.value = new Set()
    return
  }
  credentialIds.value = new Set(credentialItems.value.map(({ credential_id }) => credential_id))
}

function rejectionLabel(reason: string): string {
  return tokenLabel(reason)
}

async function submit(): Promise<void> {
  if (submitting.value) return
  feedback.value = ''
  result.value = undefined
  if (targets.value.length === 0) {
    feedback.value = t('monitor.degradation.enroll.noTargets')
    return
  }
  if (targets.value.length > degradationMaxEnrollTargets) {
    feedback.value = t('monitor.degradation.enroll.tooManyTargets', {
      max: n(degradationMaxEnrollTargets),
    })
    return
  }
  if (errors.value.size > 0) {
    showErrors.value = true
    feedback.value = t('monitor.degradation.enroll.invalid')
    return
  }
  submitting.value = true
  try {
    const enrolled = await enrollDegradationMonitors(client, {
      targets: targets.value,
      ...probeOverrides(draft.value),
      skip_existing: skipExisting.value,
    })
    result.value = enrolled
    emit('enrolled', enrolled)
    groupIds.value = []
    credentialIds.value = new Set()
  } catch {
    feedback.value = t('monitor.degradation.enroll.failed')
  } finally {
    submitting.value = false
  }
}

function updateDraft(field: DegradationProbeField, value: string): void {
  applyProbeField(draft.value, field, value)
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.degradation.enroll.title')"
    :description="t('monitor.degradation.enroll.description')"
    :close-label="t('common.close')"
    :dismissible="!submitting"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <InlineFeedback v-if="feedback" tone="danger">{{ feedback }}</InlineFeedback>
    <InlineFeedback v-else-if="result" :tone="result.rejected.length ? 'warning' : 'success'">
      {{
        t('monitor.degradation.enroll.result', {
          created: n(result.created),
          updated: n(result.updated),
          skipped: n(result.skipped),
          rejected: n(result.rejected.length),
        })
      }}
    </InlineFeedback>

    <form class="degradation-enroll" @submit.prevent="submit">
      <section class="degradation-enroll__section">
        <h3>{{ t('monitor.degradation.enroll.sections.targets') }}</h3>
        <SegmentedControl
          :model-value="mode"
          :label="t('monitor.degradation.enroll.modeLabel')"
          :options="modeOptions"
          appearance="drawer"
          @update:model-value="setMode"
        />

        <SearchableMultiSelect
          v-if="mode === 'group'"
          id="degradation-enroll-groups"
          :model-value="groupIds"
          :label="t('monitor.degradation.enroll.groups')"
          :search-label="t('monitor.degradation.enroll.groupSearch')"
          :search-placeholder="t('monitor.degradation.enroll.groupSearchPlaceholder')"
          :clear-search-label="t('monitor.degradation.enroll.clearSearch')"
          :empty-label="t('monitor.degradation.enroll.noGroups')"
          :loading-label="t('monitor.degradation.enroll.loadingGroups')"
          :selected-label="t('monitor.degradation.enroll.selectedGroups')"
          :add-label="t('monitor.degradation.enroll.addGroups')"
          :clear-label="t('monitor.degradation.enroll.clearGroups')"
          :remove-label="(label: string) => t('monitor.degradation.enroll.removeGroup', { name: label })"
          :options="groupOptions"
          :loading="groupsQuery.isPending.value"
          :disabled="submitting"
          size="compact"
          @update:model-value="groupIds = $event.map(Number)"
        />

        <template v-else>
          <FormField
            id="degradation-enroll-group"
            :label="t('monitor.degradation.enroll.group')"
            :description="t('monitor.degradation.enroll.groupHint')"
            size="compact"
          >
            <AppSelect
              id="degradation-enroll-group"
              :model-value="String(credentialGroupId)"
              :label="t('monitor.degradation.enroll.group')"
              :options="groupSelectOptions"
              :disabled="submitting || groupsQuery.isPending.value"
              size="sm"
              @update:model-value="credentialGroupId = Number($event)"
            />
          </FormField>

          <template v-if="credentialGroupId > 0">
            <div class="degradation-enroll__credential-tools">
              <AppSearchInput
                id="degradation-enroll-credential-search"
                v-model="credentialSearch"
                :label="t('monitor.degradation.enroll.credentialSearch')"
                :placeholder="t('monitor.degradation.enroll.credentialSearchPlaceholder')"
                :clear-label="t('monitor.degradation.enroll.clearSearch')"
                :disabled="submitting"
              />
              <AppButton
                variant="secondary"
                size="compact"
                :disabled="submitting || credentialItems.length === 0"
                @click="toggleAllCredentials"
              >
                {{
                  allCredentialsSelected
                    ? t('monitor.degradation.enroll.clearCredentials')
                    : t('monitor.degradation.enroll.selectAllCredentials')
                }}
              </AppButton>
            </div>
            <p v-if="credentialsQuery.isPending.value" class="degradation-enroll__note">
              {{ t('monitor.degradation.enroll.loadingCredentials') }}
            </p>
            <p v-else-if="credentialItems.length === 0" class="degradation-enroll__note">
              {{ t('monitor.degradation.enroll.noCredentials') }}
            </p>
            <ul v-else class="degradation-enroll__credentials">
              <li v-for="item in credentialItems" :key="item.credential_id">
                <label>
                  <input
                    type="checkbox"
                    :checked="credentialIds.has(item.credential_id)"
                    :disabled="submitting"
                    @change="
                      toggleCredential(
                        item.credential_id,
                        ($event.target as HTMLInputElement).checked,
                      )
                    "
                  />
                  <span class="degradation-enroll__mask">{{ item.mask }}</span>
                  <span class="degradation-enroll__credential-state">
                    {{ t(`monitor.degradation.enroll.credentialStatus.${item.effective_status}`) }}
                  </span>
                </label>
              </li>
            </ul>
            <p v-if="credentialTruncated" class="degradation-enroll__note">
              {{
                t('monitor.degradation.enroll.credentialTruncated', {
                  shown: n(credentialItems.length),
                  total: n(credentialTotal),
                })
              }}
            </p>
          </template>
        </template>

        <p class="degradation-enroll__note">{{ targetSummary }}</p>
      </section>

      <section class="degradation-enroll__section">
        <h3>{{ t('monitor.degradation.enroll.sections.probe') }}</h3>
        <DegradationProbeFields
          id-prefix="degradation-enroll"
          :draft="draft"
          :errors="errors"
          :show-errors="showErrors"
          :catalog="catalog"
          :settings="settings"
          :model-suggestions="modelSuggestions"
          :disabled="submitting"
          @update="updateDraft"
        />
        <DegradationToggleRow
          v-model="skipExisting"
          :label="t('monitor.degradation.enroll.skipExisting')"
          :description="t('monitor.degradation.enroll.skipExistingHint')"
          :disabled="submitting"
        />
      </section>

      <section v-if="result && result.rejected.length" class="degradation-enroll__section">
        <h3>{{ t('monitor.degradation.enroll.sections.rejected') }}</h3>
        <ul class="degradation-enroll__rejected">
          <li v-for="(entry, index) in result.rejected" :key="`${entry.group_id}-${entry.credential_id}-${index}`">
            <span class="degradation-enroll__mask">
              {{
                entry.credential_id > 0
                  ? t('monitor.degradation.enroll.rejectedCredential', {
                      group: n(entry.group_id),
                      credential: n(entry.credential_id),
                    })
                  : t('monitor.degradation.enroll.rejectedGroup', { group: n(entry.group_id) })
              }}
            </span>
            <span>{{ rejectionLabel(entry.reason) }}</span>
          </li>
        </ul>
      </section>
    </form>

    <template #footer>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="submitting"
        @click="emit('update:open', false)"
      >
        {{ t('common.close') }}
      </AppButton>
      <AppButton size="compact" :busy="submitting" @click="submit">
        {{ t('monitor.degradation.enroll.submit') }}
      </AppButton>
    </template>
  </AppDrawer>
</template>

<style scoped>
.degradation-enroll,
.degradation-enroll__section {
  display: grid;
  min-width: 0;
}

.degradation-enroll {
  gap: 0;
}

.degradation-enroll__section {
  gap: 12px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 16px 0;
}

.degradation-enroll__section:last-child {
  border-bottom: 0;
}

.degradation-enroll__section h3 {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 650;
}

.degradation-enroll__note {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.6;
}

.degradation-enroll__credential-tools {
  display: grid;
  align-items: center;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--space-2);
}

.degradation-enroll__credentials {
  display: grid;
  max-height: 240px;
  margin: 0;
  gap: 2px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 6px;
  overflow-y: auto;
  list-style: none;
}

.degradation-enroll__credentials label {
  display: grid;
  align-items: center;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: var(--space-2);
  border-radius: var(--radius-control);
  padding: 6px 8px;
  cursor: pointer;
}

.degradation-enroll__credentials label:hover {
  background: var(--color-surface);
}

.degradation-enroll__credentials input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}

.degradation-enroll__mask {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.degradation-enroll__credential-state {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.degradation-enroll__rejected {
  display: grid;
  margin: 0;
  gap: 4px;
  padding: 0;
  list-style: none;
}

.degradation-enroll__rejected li {
  display: grid;
  align-items: baseline;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
}

@media (max-width: 520px) {
  .degradation-enroll__credential-tools {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
