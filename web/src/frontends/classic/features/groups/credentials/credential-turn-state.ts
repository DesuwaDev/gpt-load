import { codexTurnStateTtlMs } from '@/lib/codex-turn-state'
import { parseFernetToken } from '@/lib/fernet'

// 日志抽屉也要同一块表，时钟本体挪到了 app 层；这里保留原名，凭据侧的调用方不用改。
export { useCodexTurnStateNow as useCredentialTurnStateNow } from '@/app/use-codex-turn-state-now'

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

/**
 * X-Codex-Turn-State 是标准 Fernet token，值自己带着上游的签发时刻——这比「我们什么
 * 时候把它写进库」准，也不挑存量行。不是 Fernet 结构就返回 null，由调用方决定退回
 * 哪个起点。
 */
export function credentialTurnStateIssuedAtMs(value: string): number | null {
  return parseFernetToken(value)?.issuedAtMs ?? null
}

/** 剩余时效毫秒数，负数表示已超时；没有起点时返回 null，表示无法计时。 */
export function credentialTurnStateRemainingMs(originMs: number, nowMs: number): number | null {
  if (!Number.isFinite(originMs) || originMs <= 0) return null
  return originMs + codexTurnStateTtlMs - nowMs
}
