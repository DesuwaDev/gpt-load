<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  updateDegradationMonitor,
  type DegradationCatalogDto,
  type DegradationMonitorDto,
  type DegradationSettingsDto,
} from '@/app/resources/degradation'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import DegradationProbeFields from './DegradationProbeFields.vue'
import DegradationToggleRow from './DegradationToggleRow.vue'
import {
  applyProbeField,
  emptyProbeDraft,
  monitorProbeDraft,
  monitorUpdatePayload,
  probeDraftErrors,
  type DegradationProbeDraft,
  type DegradationProbeField,
} from './degradation-form'

const props = defineProps<{
  open: boolean
  monitor?: DegradationMonitorDto
  settings: DegradationSettingsDto
  catalog: DegradationCatalogDto
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  saved: [monitor: DegradationMonitorDto]
}>()
const client = useApiClient()
const { t } = useI18n()

const draft = ref<DegradationProbeDraft>(emptyProbeDraft())
const enabled = ref(true)
const showErrors = ref(false)
const saving = ref(false)
const feedback = ref('')

// 抽屉复用同一个实例，换监控项或重新打开都要把草稿重建回服务端状态。
watch(
  [() => props.open, () => props.monitor?.id, () => props.monitor?.updated_at_ms],
  () => {
    if (!props.open || !props.monitor) return
    draft.value = monitorProbeDraft(props.monitor)
    enabled.value = props.monitor.enabled
    showErrors.value = false
    feedback.value = ''
  },
  { immediate: true },
)

const errors = computed(() => probeDraftErrors(draft.value, props.catalog.max_samples))
const targetLabel = computed(() => {
  const monitor = props.monitor
  if (!monitor) return ''
  return monitor.credential_id > 0
    ? `${monitor.group_name} · ${monitor.credential_mask}`
    : monitor.group_name
})
const modelSuggestions = computed(() =>
  props.monitor?.upstream_model ? [props.monitor.upstream_model] : [],
)

function updateDraft(field: DegradationProbeField, value: string): void {
  applyProbeField(draft.value, field, value)
}

async function save(): Promise<void> {
  const monitor = props.monitor
  if (!monitor || saving.value) return
  if (errors.value.size > 0) {
    showErrors.value = true
    feedback.value = t('monitor.degradation.monitorForm.invalid')
    return
  }
  saving.value = true
  feedback.value = ''
  try {
    const saved = await updateDegradationMonitor(
      client,
      monitor.id,
      monitorUpdatePayload(draft.value, enabled.value),
    )
    emit('saved', saved)
    emit('update:open', false)
  } catch {
    feedback.value = t('monitor.degradation.monitorForm.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.degradation.monitorForm.title')"
    :description="targetLabel || t('monitor.degradation.monitorForm.description')"
    :close-label="t('common.close')"
    :dismissible="!saving"
    show-description
    @update:open="emit('update:open', $event)"
  >
    <InlineFeedback v-if="feedback" tone="danger">{{ feedback }}</InlineFeedback>
    <form v-if="monitor" class="degradation-monitor-form" @submit.prevent="save">
      <DegradationToggleRow
        v-model="enabled"
        :label="t('monitor.degradation.monitorForm.enabled')"
        :description="t('monitor.degradation.monitorForm.enabledHint')"
        :disabled="saving"
      />
      <DegradationProbeFields
        id-prefix="degradation-monitor"
        :draft="draft"
        :errors="errors"
        :show-errors="showErrors"
        :catalog="catalog"
        :settings="settings"
        :model-suggestions="modelSuggestions"
        :disabled="saving"
        @update="updateDraft"
      />
    </form>

    <template #footer>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="saving"
        @click="emit('update:open', false)"
      >
        {{ t('common.cancel') }}
      </AppButton>
      <AppButton size="compact" :busy="saving" :disabled="!monitor" @click="save">
        {{ t('monitor.degradation.monitorForm.save') }}
      </AppButton>
    </template>
  </AppDrawer>
</template>

<style scoped>
.degradation-monitor-form {
  display: grid;
  min-width: 0;
  gap: 16px;
  padding: 16px 0;
}
</style>
