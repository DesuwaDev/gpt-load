<script setup lang="ts">
import { ChevronDown, Pipette, X } from '@lucide/vue'
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
import AppPopover from '@/components/ui/AppPopover.vue'
import CopyFallbackDialog from '@/components/ui/CopyFallbackDialog.vue'
import {
  codexTurnStateShapeOrder,
  codexTurnStateShapes,
  type CodexTurnStateShape,
} from '@/lib/codex-turn-state'

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
const menuOpen = ref(false)
const scanning = ref<CodexTurnStateShape | null>(null)
const feedback = ref<{ tone: 'ok' | 'warn' | 'error'; lines: string[] } | null>(null)

function shapeLabel(shape: CodexTurnStateShape): string {
  return t(`monitor.logs.turnStatePick.shape.${shape}`)
}

function credentialLabel(name: string): string {
  return name === '' ? t('monitor.logs.turnStatePick.unknownCredential') : name
}

async function pick(shape: CodexTurnStateShape): Promise<void> {
  if (scanning.value !== null) return
  scanning.value = shape
  feedback.value = null
  const chars = codexTurnStateShapes[shape].chars
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
      feedback.value = { tone: 'warn', lines: [t('monitor.logs.turnStatePick.noLogs')] }
      toast.show({
        message: t('monitor.logs.turnStatePick.noLogs'),
        tone: 'warning',
      })
      return
    }
    const details: RequestLogDetailDto[] = []
    let candidates = collectTurnStateCandidates(details, shape)
    for (let index = 0; index < items.length; index += batchSize) {
      const batch = items.slice(index, index + batchSize)
      details.push(
        ...(await Promise.all(
          batch.map((item) => getRequestLog(client, item.request_id, controller.signal)),
        )),
      )
      candidates = collectTurnStateCandidates(details, shape)
      // 列表本来就是从新到旧，命中就收手：再往回翻只会拿到签发更早的值。
      if (candidates.length > 0) break
    }
    const best = candidates[0]
    if (best === undefined) {
      const msg = t('monitor.logs.turnStatePick.none', {
        scanned: details.length,
        chars,
        shape: shapeLabel(shape),
      })
      feedback.value = {
        tone: 'warn',
        lines: [msg],
      }
      toast.show({
        message: t('monitor.logs.turnStatePick.toastNone', {
          shape: shapeLabel(shape),
          chars,
        }),
        tone: 'warning',
      })
      return
    }
    const result = await copy(best.value)
    if (result === 'cancelled') return
    const lines = [
      t(`monitor.logs.turnStatePick.${result === 'success' ? 'copied' : 'found'}`, {
        chars,
        shape: shapeLabel(shape),
      }),
      t('monitor.logs.turnStatePick.detail', {
        credential: credentialLabel(best.credentialName),
        scanned: details.length,
        total: candidates.length,
      }),
    ]
    // 跨凭据时「最新」未必是用户想要的那个账号，把这件事挑明，别让它默默替人做决定。
    const credentials = countTurnStateCandidateCredentials(candidates)
    if (credentials > 1) {
      lines.push(t('monitor.logs.turnStatePick.spread', { total: candidates.length, credentials }))
    }
    feedback.value = { tone: 'ok', lines }
    if (result === 'success') {
      toast.show({
        message: t('monitor.logs.turnStatePick.toastCopied', {
          shape: shapeLabel(shape),
          chars,
        }),
        tone: 'success',
      })
    }
  } catch {
    if (controller.signal.aborted) return
    const failMsg = t('monitor.logs.turnStatePick.failed')
    feedback.value = { tone: 'error', lines: [failMsg] }
    toast.show({ message: failMsg, tone: 'danger' })
  } finally {
    pool.release(controller)
    scanning.value = null
  }
}
</script>

<template>
  <span class="turn-state-pick">
    <AppPopover v-model:open="menuOpen" align="end" content-class="turn-state-pick__popover">
      <template #trigger>
        <AppButton
          class="turn-state-pick__trigger"
          variant="secondary"
          size="compact"
          :busy="scanning !== null || (pending && scanning === null)"
          :title="t('monitor.logs.turnStatePick.triggerHint')"
        >
          <Pipette :size="14" aria-hidden="true" />
          <span>{{
            scanning !== null
              ? t('monitor.logs.turnStatePick.scanning')
              : t('monitor.logs.turnStatePick.trigger')
          }}</span>
          <ChevronDown :size="12" class="turn-state-pick__chevron" aria-hidden="true" />
        </AppButton>
      </template>

      <div class="turn-state-pick__menu">
        <div class="turn-state-pick__menu-header">
          <span class="turn-state-pick__menu-title">{{
            t('monitor.logs.turnStatePick.menuTitle')
          }}</span>
          <span class="turn-state-pick__menu-hint">{{
            t('monitor.logs.turnStatePick.menuHint')
          }}</span>
        </div>

        <div class="turn-state-pick__options">
          <button
            v-for="shape in codexTurnStateShapeOrder"
            :key="shape"
            type="button"
            class="turn-state-pick__option"
            :disabled="scanning !== null"
            @click="pick(shape)"
          >
            <div class="turn-state-pick__option-main">
              <span class="turn-state-pick__option-name">{{ shapeLabel(shape) }}</span>
              <span class="turn-state-pick__option-chars"
                >{{ codexTurnStateShapes[shape].chars }} 字符</span
              >
            </div>
            <div class="turn-state-pick__option-desc">
              {{ codexTurnStateShapes[shape].blocks }} 块密文 ·
              {{
                shape === 'individual'
                  ? t('monitor.logs.turnStatePick.individualDesc')
                  : t('monitor.logs.turnStatePick.teamDesc')
              }}
            </div>
          </button>
        </div>

        <div
          v-if="feedback"
          class="turn-state-pick__feedback"
          :class="`turn-state-pick__feedback--${feedback.tone}`"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          <div class="turn-state-pick__lines">
            <span v-for="line in feedback.lines" :key="line">{{ line }}</span>
          </div>
          <button
            type="button"
            class="turn-state-pick__dismiss"
            :aria-label="t('monitor.logs.turnStatePick.dismiss')"
            @click="feedback = null"
          >
            <X :size="14" aria-hidden="true" />
          </button>
        </div>
      </div>
    </AppPopover>

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

.turn-state-pick__chevron {
  opacity: 0.7;
}

:global(.turn-state-pick__popover) {
  width: 270px;
  max-width: min(320px, calc(100vw - 2 * var(--space-3)));
  padding: var(--space-3);
}

.turn-state-pick__menu {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.turn-state-pick__menu-header {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: var(--space-2);
  border-bottom: 1px solid var(--color-border-subtle);
}

.turn-state-pick__menu-title {
  font-size: var(--text-sm);
  font-weight: 650;
  color: var(--color-text);
}

.turn-state-pick__menu-hint {
  font-size: var(--text-xs);
  color: var(--color-text-faint);
  line-height: 1.3;
}

.turn-state-pick__options {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.turn-state-pick__option {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  text-align: left;
  transition: all var(--duration-fast) var(--easing-standard);
}

.turn-state-pick__option:hover:not(:disabled) {
  background: var(--color-surface-hover);
  border-color: var(--color-border);
}

.turn-state-pick__option:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.turn-state-pick__option-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.turn-state-pick__option-name {
  font-size: var(--text-sm);
  font-weight: 600;
}

.turn-state-pick__option-chars {
  font-size: var(--text-label-xs);
  font-family: var(--font-mono);
  padding: 1px 6px;
  border-radius: var(--radius-tag);
  background: var(--color-action-soft);
  color: var(--color-action);
  font-weight: 600;
}

.turn-state-pick__option-desc {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.turn-state-pick__feedback {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-2);
  font-size: var(--text-xs);
  line-height: 1.4;
  text-align: left;
}

.turn-state-pick__feedback--ok {
  border-color: var(--color-success);
  background: var(--color-success-bg, var(--color-surface));
  color: var(--color-success);
}

.turn-state-pick__feedback--warn {
  border-color: var(--color-warning);
  background: var(--color-warning-bg, var(--color-surface));
  color: var(--color-warning);
}

.turn-state-pick__feedback--error {
  border-color: var(--color-danger);
  background: var(--color-danger-bg, var(--color-surface));
  color: var(--color-danger);
}

.turn-state-pick__lines {
  display: grid;
  gap: 2px;
  overflow-wrap: anywhere;
  flex: 1;
}

.turn-state-pick__dismiss {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-tag);
  background: none;
  color: inherit;
  padding: 2px;
  cursor: pointer;
}
</style>
