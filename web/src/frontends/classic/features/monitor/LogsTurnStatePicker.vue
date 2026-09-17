<script setup lang="ts">
import { Pipette, X } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@shared/http/client-context'
import {
  getRequestLog,
  listRequestLogs,
  type RequestLogDetailDto,
  type RequestLogFilters,
} from '@/app/resources/request-logs'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import { useClipboardCopy } from '@/app/use-clipboard-copy'
import AppButton from '@/components/ui/AppButton.vue'
import CopyFallbackDialog from '@/components/ui/CopyFallbackDialog.vue'
import {
  codexTurnStateShapeOrder,
  codexTurnStateShapes,
  codexTurnStateTtlMs,
  formatCodexTurnStateDuration,
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
const { copy, fallbackText, pending, reset } = useClipboardCopy()
const { t } = useI18n()
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
    const nowMs = Date.now()
    // 轮次状态只活 1 小时，更早的日志里不可能有还没过期的值——所以时间范围固定取最近
    // 1 小时，不跟随上面已应用的那个范围；分组、凭据、模型等其余筛选条件照用。
    const page = await listRequestLogs(
      client,
      { ...props.filters, from_ms: nowMs - codexTurnStateTtlMs, to_ms: nowMs, limit: listSize },
      undefined,
      controller.signal,
    )
    const items = page.items.slice(0, scanLimit)
    if (items.length === 0) {
      feedback.value = { tone: 'warn', lines: [t('monitor.logs.turnStatePick.noLogs')] }
      return
    }
    const details: RequestLogDetailDto[] = []
    let candidates = collectTurnStateCandidates(details, nowMs, shape)
    for (let index = 0; index < items.length; index += batchSize) {
      const batch = items.slice(index, index + batchSize)
      details.push(
        ...(await Promise.all(
          batch.map((item) => getRequestLog(client, item.request_id, controller.signal)),
        )),
      )
      candidates = collectTurnStateCandidates(details, nowMs, shape)
      // 列表本来就是从新到旧，命中就收手：再往回翻只会拿到签发更早、剩得更少的值。
      if (candidates.length > 0) break
    }
    const best = candidates[0]
    if (best === undefined) {
      feedback.value = {
        tone: 'warn',
        lines: [
          t('monitor.logs.turnStatePick.none', {
            scanned: details.length,
            chars,
            shape: shapeLabel(shape),
          }),
        ],
      }
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
        duration: formatCodexTurnStateDuration(best.remainingMs),
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
  } catch {
    if (controller.signal.aborted) return
    feedback.value = { tone: 'error', lines: [t('monitor.logs.turnStatePick.failed')] }
  } finally {
    pool.release(controller)
    scanning.value = null
  }
}
</script>

<template>
  <span class="turn-state-pick">
    <!-- 两种形态分开取：个人号和 team 号的状态不能互换着注入，合成一个按钮只会让人误用。 -->
    <AppButton
      v-for="shape in codexTurnStateShapeOrder"
      :key="shape"
      variant="secondary"
      size="compact"
      :busy="scanning === shape || (pending && scanning === null)"
      :disabled="scanning !== null && scanning !== shape"
      :title="
        t('monitor.logs.turnStatePick.hint', {
          chars: codexTurnStateShapes[shape].chars,
          blocks: codexTurnStateShapes[shape].blocks,
          shape: shapeLabel(shape),
        })
      "
      @click="pick(shape)"
    >
      <Pipette :size="14" aria-hidden="true" />
      {{ t('monitor.logs.turnStatePick.button', { chars: codexTurnStateShapes[shape].chars }) }}
    </AppButton>
    <span
      v-if="feedback"
      class="turn-state-pick__feedback"
      :class="`turn-state-pick__feedback--${feedback.tone}`"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      <span class="turn-state-pick__lines">
        <span v-for="line in feedback.lines" :key="line">{{ line }}</span>
      </span>
      <button
        type="button"
        class="turn-state-pick__dismiss"
        :aria-label="t('monitor.logs.turnStatePick.dismiss')"
        @click="feedback = null"
      >
        <X :size="14" aria-hidden="true" />
      </button>
    </span>
    <CopyFallbackDialog v-if="fallbackText !== undefined" :value="fallbackText" @close="reset" />
  </span>
</template>

<style scoped>
.turn-state-pick {
  position: relative;
  display: inline-flex;
  gap: var(--space-1);
}
/* 结果有两三句话，撑不进一行徽章，所以做成一张跟着这组按钮左缘展开的浮层。两个按钮共用
   一块结果区——同时只会有一次扫描在跑，分成两块只是把同一条消息挪来挪去。不像普通的
   复制反馈那样几秒后自动消失：这里要读的信息量足够大，留到下次扫描或手动关掉为止。 */
.turn-state-pick__feedback {
  position: absolute;
  z-index: var(--z-popover);
  top: calc(100% + var(--space-1));
  left: 0;
  display: flex;
  width: max-content;
  max-width: min(360px, calc(100vw - 2 * var(--space-4)));
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-2);
  box-shadow: var(--shadow-card);
  font-size: 0.75rem;
  line-height: 1.5;
  text-align: left;
  white-space: normal;
}
.turn-state-pick__feedback--warn {
  border-color: var(--color-warning);
  color: var(--color-warning);
}
.turn-state-pick__feedback--error {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
.turn-state-pick__lines {
  display: grid;
  gap: 2px;
  overflow-wrap: anywhere;
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
@media (max-width: 560px) {
  /* 竖屏里这一排按钮会折行，浮层靠视口宽度收口才不会越过右边界；关闭键也要够拇指点。 */
  .turn-state-pick__feedback {
    max-width: calc(100vw - 2 * var(--space-3));
  }
  .turn-state-pick__dismiss {
    min-width: var(--touch-target);
    min-height: var(--touch-target);
    margin: calc(-1 * var(--space-1)) calc(-1 * var(--space-1)) 0 0;
  }
}
</style>
