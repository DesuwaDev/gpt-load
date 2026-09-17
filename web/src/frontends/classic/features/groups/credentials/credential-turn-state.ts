import { onScopeDispose, ref, type Ref } from 'vue'

/** 注入值要原样进 HTTP 头，长度与字符集与服务端的 validCodexTurnState 保持一致。 */
export const credentialTurnStateMaxLength = 4096

export function validCredentialTurnState(value: string): boolean {
  return (
    value.length <= credentialTurnStateMaxLength &&
    ![...value].some((char) => char < ' ' || char > '~')
  )
}

/** 模型名单的列宽，与服务端的 maxCodexTurnStateModelsBytes 一致。 */
export const credentialTurnStateModelsMaxLength = 1024

/**
 * 与服务端的 canonicalCodexTurnStateModels 同口径：去空白、小写、去重、逗号分隔。
 * 规范化后再提交，界面上看到的就是库里存的，不会出现「改完又被后端改一次」。
 * 返回 null 表示名单非法。
 */
export function canonicalCredentialTurnStateModels(raw: string): string | null {
  const entries: string[] = []
  const seen = new Set<string>()
  for (const part of raw.split(',')) {
    const entry = part.trim().toLowerCase()
    if (entry === '') continue
    if (!validCredentialTurnStateModelEntry(entry)) return null
    if (seen.has(entry)) continue
    seen.add(entry)
    entries.push(entry)
  }
  const value = entries.join(',')
  return value.length > credentialTurnStateModelsMaxLength ? null : value
}

/** 条目只允许模型名里真会出现的字符，外加结尾的 * 做前缀匹配。 */
function validCredentialTurnStateModelEntry(entry: string): boolean {
  return /^[a-z0-9\-_.:/]+\*?$/.test(entry)
}

/** 上游签发的 X-Codex-Turn-State 实测约 1 小时后失效。超时只提醒，不停注入。 */
export const credentialTurnStateTtlMs = 60 * 60 * 1000

// 凭据列表里可能同时挂着几十个编辑器，共用一个秒级时钟，免得每行各起一个定时器。
const sharedNowMs = ref(Date.now())
let sharedTimer: ReturnType<typeof setInterval> | null = null
let sharedClockUsers = 0

/** 订阅共享时钟；调用方所在的 effect scope 销毁时自动退订，最后一个退订者停表。 */
export function useCredentialTurnStateNow(): Ref<number> {
  sharedClockUsers += 1
  if (sharedTimer === null) {
    sharedNowMs.value = Date.now()
    sharedTimer = setInterval(() => {
      sharedNowMs.value = Date.now()
    }, 1000)
  }
  onScopeDispose(() => {
    sharedClockUsers -= 1
    if (sharedClockUsers > 0 || sharedTimer === null) return
    clearInterval(sharedTimer)
    sharedTimer = null
  })
  return sharedNowMs
}

/** 剩余时效毫秒数，负数表示已超时；没有写入时刻时返回 null，表示无法计时。 */
export function credentialTurnStateRemainingMs(setAtMs: number, nowMs: number): number | null {
  if (!Number.isFinite(setAtMs) || setAtMs <= 0) return null
  return setAtMs + credentialTurnStateTtlMs - nowMs
}

/** 把时长渲染成 mm:ss，跨过一小时的部分再补上小时位。只取绝对值，方向由文案给。 */
export function formatCredentialTurnStateDuration(ms: number): string {
  const total = Math.floor(Math.abs(ms) / 1000)
  const pad = (value: number): string => String(value).padStart(2, '0')
  const seconds = total % 60
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`
}
