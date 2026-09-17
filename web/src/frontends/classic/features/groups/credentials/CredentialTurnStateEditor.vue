<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { codexTurnStateTtlMs, formatCodexTurnStateDuration } from '@/lib/codex-turn-state'

import {
  canonicalCredentialTurnStateModels,
  credentialTurnStateIssuedAtMs,
  credentialTurnStateMaxLength,
  credentialTurnStateModelsMaxLength,
  credentialTurnStateRemainingMs,
  useCredentialTurnStateNow,
  validCredentialTurnState,
} from './credential-turn-state'

const props = defineProps<{
  value: string
  models: string
  setAtMs: number
  disabled?: boolean
}>()
const emit = defineEmits<{
  apply: [payload: { codex_turn_state: string; codex_turn_state_models: string }]
}>()

const { n, t } = useI18n()

const draft = ref(props.value)
const modelsDraft = ref(props.models)

// 菜单实例在行间复用，上游刷新出新值时要把草稿拉回当前值。
watch(
  () => props.value,
  (value) => {
    draft.value = value
  },
)
watch(
  () => props.models,
  (value) => {
    modelsDraft.value = value
  },
)

const trimmed = computed(() => draft.value.trim())
const invalid = computed(() => !validCredentialTurnState(trimmed.value))
const canonicalModels = computed(() => canonicalCredentialTurnStateModels(modelsDraft.value))
const modelsInvalid = computed(() => canonicalModels.value === null)
const dirty = computed(
  () => trimmed.value !== props.value || canonicalModels.value !== props.models,
)
const canApply = computed(
  () => !props.disabled && !invalid.value && !modelsInvalid.value && dirty.value,
)

// 时效倒计时纯属提醒：超时既不停用凭据，也不停止注入，只是提示该换一个 state 了。
const nowMs = useCredentialTurnStateNow()
// 起点优先从值本身读：Fernet token 自带签发时刻，比「什么时候保存的」准，而且刚粘进来
// 还没保存也能立刻开始计时。读不出来才退回保存时刻，且只在草稿正是已保存的那个值时才
// 退——草稿换了内容的话，那个时刻描述的是旧值，不是眼前这一个。
const origin = computed<{ ms: number; exact: boolean } | null>(() => {
  if (trimmed.value === '') return null
  const issuedAtMs = credentialTurnStateIssuedAtMs(trimmed.value)
  if (issuedAtMs !== null) return { ms: issuedAtMs, exact: true }
  if (trimmed.value === props.value && props.setAtMs > 0) return { ms: props.setAtMs, exact: false }
  return null
})
const remainingMs = computed(() =>
  origin.value === null ? null : credentialTurnStateRemainingMs(origin.value.ms, nowMs.value),
)
const expired = computed(() => remainingMs.value !== null && remainingMs.value <= 0)
// 剩余不足十分钟就转成告警色，留出换值的余量。
const ttlTone = computed(() => {
  if (remainingMs.value === null) return 'unknown'
  if (remainingMs.value <= 0) return 'expired'
  return remainingMs.value <= 10 * 60 * 1000 ? 'soon' : 'ok'
})
const ttlText = computed(() => {
  if (remainingMs.value === null) return t('group.credentials.turnState.ttlUnknown')
  // 估出来的倒计时前面挂个 ≈，免得看着像从值里读出来的那种精确。
  const approx = origin.value?.exact === false ? '≈' : ''
  const duration = approx + formatCodexTurnStateDuration(remainingMs.value)
  return remainingMs.value <= 0
    ? t('group.credentials.turnState.ttlExpired', { duration })
    : t('group.credentials.turnState.ttlRemaining', { duration })
})
// 悬停时说清这个倒计时是从哪儿算起的，省得为一个数字去猜。
const ttlTitle = computed(() => {
  if (origin.value === null) return t('group.credentials.turnState.ttlUnknownHint')
  return origin.value.exact
    ? t('group.credentials.turnState.ttlIssuedAt', {
        time: new Date(origin.value.ms).toLocaleString(),
      })
    : t('group.credentials.turnState.ttlApprox')
})
const ttlPercent = computed(() => {
  if (remainingMs.value === null) return 0
  const ratio = remainingMs.value / codexTurnStateTtlMs
  return Math.min(100, Math.max(0, Math.round(ratio * 100)))
})

function apply(): void {
  if (!canApply.value || canonicalModels.value === null) return
  emit('apply', {
    codex_turn_state: trimmed.value,
    codex_turn_state_models: canonicalModels.value,
  })
}

// 清除只关注入值，模型名单原样留着，方便换一个 state 之后继续复用同一份范围。
function clear(): void {
  if (props.disabled || props.value === '') return
  draft.value = ''
  emit('apply', { codex_turn_state: '', codex_turn_state_models: props.models })
}
</script>

<template>
  <div class="credential-turn-state">
    <p class="credential-turn-state__title">{{ t('group.credentials.turnState.title') }}</p>
    <p class="credential-turn-state__hint">{{ t('group.credentials.turnState.hint') }}</p>
    <textarea
      v-model="draft"
      class="credential-turn-state__input"
      :class="{ 'credential-turn-state__input--invalid': invalid }"
      rows="3"
      spellcheck="false"
      :maxlength="credentialTurnStateMaxLength"
      :disabled="disabled"
      :placeholder="t('group.credentials.turnState.placeholder')"
      :aria-label="t('group.credentials.turnState.title')"
      :aria-invalid="invalid"
    ></textarea>
    <p class="credential-turn-state__status">
      <span
        class="credential-turn-state__state"
        :class="
          value === '' ? 'credential-turn-state__state--off' : 'credential-turn-state__state--on'
        "
      >
        {{
          value === ''
            ? t('group.credentials.turnState.inactive')
            : t('group.credentials.turnState.active')
        }}
      </span>
      <span
        v-if="trimmed !== ''"
        class="credential-turn-state__ttl"
        :class="'credential-turn-state__ttl--' + ttlTone"
        :title="ttlTitle"
      >
        {{ ttlText }}
      </span>
      <span v-if="invalid" class="credential-turn-state__error">
        {{ t('group.credentials.turnState.invalid') }}
      </span>
      <span v-else class="credential-turn-state__length">
        {{ t('group.credentials.turnState.length', { count: n(trimmed.length) }) }}
      </span>
    </p>
    <!-- 细条只是把「还剩多少」放大成一眼可扫的形状，信息本身在上面的文字里。 -->
    <div v-if="remainingMs !== null" class="credential-turn-state__ttl-track" aria-hidden="true">
      <span
        class="credential-turn-state__ttl-fill"
        :class="'credential-turn-state__ttl-fill--' + ttlTone"
        :style="{ width: ttlPercent + '%' }"
      ></span>
    </div>
    <p v-if="expired" class="credential-turn-state__notice">
      {{ t('group.credentials.turnState.ttlNotice') }}
    </p>
    <p class="credential-turn-state__hint">{{ t('group.credentials.turnState.modelsHint') }}</p>
    <input
      v-model="modelsDraft"
      class="credential-turn-state__input"
      :class="{ 'credential-turn-state__input--invalid': modelsInvalid }"
      type="text"
      spellcheck="false"
      autocomplete="off"
      :maxlength="credentialTurnStateModelsMaxLength"
      :disabled="disabled"
      :placeholder="t('group.credentials.turnState.modelsPlaceholder')"
      :aria-label="t('group.credentials.turnState.models')"
      :aria-invalid="modelsInvalid"
    />
    <p class="credential-turn-state__status">
      <span
        class="credential-turn-state__state"
        :class="
          canonicalModels === ''
            ? 'credential-turn-state__state--off'
            : 'credential-turn-state__state--on'
        "
      >
        {{
          canonicalModels === ''
            ? t('group.credentials.turnState.modelsAll')
            : t('group.credentials.turnState.modelsScoped')
        }}
      </span>
      <span v-if="modelsInvalid" class="credential-turn-state__error">
        {{ t('group.credentials.turnState.modelsInvalid') }}
      </span>
    </p>
    <div class="credential-turn-state__actions">
      <button
        class="credential-turn-state__clear"
        type="button"
        :disabled="disabled || value === ''"
        @click="clear"
      >
        {{ t('group.credentials.turnState.clear') }}
      </button>
      <button
        class="credential-turn-state__apply"
        type="button"
        :disabled="!canApply"
        @click="apply"
      >
        {{ t('group.credentials.turnState.apply') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.credential-turn-state {
  display: grid;
  gap: 5px;
  padding: 2px 6px 6px;
}
.credential-turn-state__title {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 650;
  letter-spacing: 0.02em;
}
.credential-turn-state__hint {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
/* 实测的 state 在 300 字符上下，三行等宽足够读出开头和结尾的差异。 */
.credential-turn-state__input {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 4px 6px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: 1.5;
  resize: vertical;
  overflow-wrap: anywhere;
}
.credential-turn-state__input:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}
.credential-turn-state__input--invalid {
  border-color: var(--color-danger);
}
.credential-turn-state__status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  margin: 0;
  font-size: var(--text-label-xs);
}
.credential-turn-state__state {
  font-weight: 650;
}
.credential-turn-state__state--on {
  color: var(--color-info);
}
.credential-turn-state__state--off {
  color: var(--color-text-faint);
}
.credential-turn-state__ttl {
  font-variant-numeric: tabular-nums;
  font-weight: 650;
}
.credential-turn-state__ttl--ok {
  color: var(--color-text-muted);
}
.credential-turn-state__ttl--soon {
  color: var(--color-warning);
}
.credential-turn-state__ttl--expired {
  color: var(--color-danger);
}
.credential-turn-state__ttl--unknown {
  color: var(--color-text-faint);
  font-weight: inherit;
}
.credential-turn-state__ttl-track {
  height: 3px;
  border-radius: 999px;
  background: var(--color-border-control);
  overflow: hidden;
}
.credential-turn-state__ttl-fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  transition: width 0.9s linear;
}
.credential-turn-state__ttl-fill--ok {
  background: var(--color-info);
}
.credential-turn-state__ttl-fill--soon {
  background: var(--color-warning);
}
.credential-turn-state__ttl-fill--expired {
  background: var(--color-danger);
}
.credential-turn-state__notice {
  margin: 0;
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-danger);
  padding: 3px 6px;
  font-size: var(--text-label-xs);
  line-height: 1.5;
}
.credential-turn-state__length {
  margin-left: auto;
  color: var(--color-text-faint);
}
.credential-turn-state__error {
  margin-left: auto;
  color: var(--color-danger);
}
.credential-turn-state__actions {
  display: flex;
  gap: 4px;
}
.credential-turn-state .credential-turn-state__clear,
.credential-turn-state .credential-turn-state__apply {
  flex: 1;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 3px 8px;
  font: inherit;
  font-size: var(--text-label-xs);
  font-weight: 650;
  line-height: 1.6;
  cursor: pointer;
}
.credential-turn-state .credential-turn-state__apply {
  border-color: transparent;
  background: var(--color-info);
  color: var(--color-text-inverse);
}
.credential-turn-state .credential-turn-state__clear:disabled,
.credential-turn-state .credential-turn-state__apply:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.credential-turn-state .credential-turn-state__clear:focus-visible,
.credential-turn-state .credential-turn-state__apply:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}

/* 竖屏手机上这个菜单只有三百来像素宽，且全靠拇指操作：放宽输入区，把按钮抬到能点中的高度。 */
@media (max-width: 560px) {
  .credential-turn-state {
    gap: 7px;
  }

  .credential-turn-state__input {
    padding: 6px 8px;
  }

  .credential-turn-state__status {
    gap: 3px 8px;
  }

  /* 窄屏里状态行必然折行，这时候再把字数推到右端只会和上一行对不齐。 */
  .credential-turn-state__length,
  .credential-turn-state__error {
    margin-left: 0;
  }

  .credential-turn-state .credential-turn-state__clear,
  .credential-turn-state .credential-turn-state__apply {
    min-height: var(--touch-target);
  }
}
</style>
