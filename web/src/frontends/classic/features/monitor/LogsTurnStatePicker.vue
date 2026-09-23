<script setup lang="ts">
import { Pipette } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@shared/http/client-context'
import {
  getRequestLog,
  listRequestLogs,
  type RequestLogDetailDto,
  type RequestLogFilters,
} from '@/app/resources/request-logs'
import { useToast } from '@/app/toast'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import { useClipboardCopy } from '@/app/use-clipboard-copy'
import AppButton from '@/components/ui/AppButton.vue'
import CopyFallbackDialog from '@/components/ui/CopyFallbackDialog.vue'

import { collectTurnStateCandidates, countTurnStateCandidateCredentials } from './turn-state-pick'

const props = defineProps<{ filters: RequestLogFilters }>()

/** 列表一次要回多少条。轮次状态只在详情接口里，列表行本身拿不到。 */
const listSize = 50
/** 最多翻多少条详情。命中通常落在第一批，这个上限只用来兜住「一条都没有」的情况。 */
const scanLimit = 30
/** 每批并发多少个详情请求。 */
const batchSize = 6

const client = useApiClient()
const pool = useAbortControllerPool()
const toast = useToast()
const { copy, fallbackText, pending, reset } = useClipboardCopy()
const { t } = useI18n()
const scanning = ref(false)

function credentialLabel(name: string): string {
  return name === '' ? t('monitor.logs.turnStatePick.unknownCredential') : name
}

async function pick(): Promise<void> {
  if (scanning.value) return
  scanning.value = true
  const controller = pool.create()
  try {
    const page = await listRequestLogs(
      client,
      { ...props.filters, limit: listSize },
      undefined,
      controller.signal,
    )
    const items = page.items.slice(0, scanLimit)
    if (items.length === 0) {
      toast.show({
        message: t('monitor.logs.turnStatePick.noLogs'),
        tone: 'warning',
      })
      return
    }
    const details: RequestLogDetailDto[] = []
    let candidates = collectTurnStateCandidates(details)
    for (let index = 0; index < items.length; index += batchSize) {
      const batch = items.slice(index, index + batchSize)
      details.push(
        ...(await Promise.all(
          batch.map((item) => getRequestLog(client, item.request_id, controller.signal)),
        )),
      )
      candidates = collectTurnStateCandidates(details)
      // 列表从新到旧，命中就收手：再往回翻只会拿到签发更早的值。
      if (candidates.length > 0) break
    }
    const best = candidates[0]
    if (best === undefined) {
      toast.show({
        message: t('monitor.logs.turnStatePick.none', { scanned: details.length }),
        tone: 'warning',
      })
      return
    }
    const result = await copy(best.value)
    if (result === 'cancelled') return
    if (result === 'success') {
      const credentials = countTurnStateCandidateCredentials(candidates)
      const message =
        credentials > 1
          ? t('monitor.logs.turnStatePick.toastCopiedSpread', {
              chars: best.value.length,
              credential: credentialLabel(best.credentialName),
              total: candidates.length,
              credentials,
            })
          : t('monitor.logs.turnStatePick.toastCopied', {
              chars: best.value.length,
              credential: credentialLabel(best.credentialName),
            })
      toast.show({ message, tone: 'success' })
    }
  } catch {
    if (controller.signal.aborted) return
    toast.show({ message: t('monitor.logs.turnStatePick.failed'), tone: 'danger' })
  } finally {
    pool.release(controller)
    scanning.value = false
  }
}
</script>

<template>
  <span class="turn-state-pick">
    <AppButton
      class="turn-state-pick__trigger"
      variant="secondary"
      size="compact"
      :busy="scanning || (pending && !scanning)"
      :title="t('monitor.logs.turnStatePick.triggerHint')"
      @click="pick"
    >
      <Pipette :size="14" aria-hidden="true" />
      <span>{{
        scanning
          ? t('monitor.logs.turnStatePick.scanning')
          : t('monitor.logs.turnStatePick.trigger')
      }}</span>
    </AppButton>

    <CopyFallbackDialog v-if="fallbackText !== undefined" :value="fallbackText" @close="reset" />
  </span>
</template>

<style scoped>
.turn-state-pick {
  display: inline-flex;
}

.turn-state-pick__trigger {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}
</style>
