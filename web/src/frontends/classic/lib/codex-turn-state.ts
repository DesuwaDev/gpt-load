import { fernetBlockBytes, type FernetToken } from '@/lib/fernet'

/**
 * 实测基线：同一账号连着发的两条请求里，正常那条的轮次状态密文是 10 块（明文 144–159
 * 字节），出问题那条是 11 块（明文 160–175 字节）。多出来的这一块是不解密就能看到的
 * 唯一差异，所以拿它当疑似降智的判据。
 *
 * 要清楚这个判据的强度：样本很少，而且 PKCS7 填充下块数只能把明文框进一个 16 字节窗口，
 * 多一块只说明明文跨过了一次边界。上游换了状态结构就要重新标定——常量单独放在这里，
 * 就是为了到时候好找。
 */
export const codexTurnStateBaselineBlocks = 10

/** 基线块数对应的明文上界，用来在说明里给出「正常应该在多少字节以内」。 */
export const codexTurnStateBaselineMaxPlaintextBytes =
  codexTurnStateBaselineBlocks * fernetBlockBytes - 1

/** normal 落在基线内，suspect 超出基线，unknown 是读不出 Fernet 结构、无从判定。 */
export type CodexTurnStateVerdict = 'normal' | 'suspect' | 'unknown'

export function codexTurnStateVerdict(token: FernetToken | null): CodexTurnStateVerdict {
  if (token === null) return 'unknown'
  return token.blocks > codexTurnStateBaselineBlocks ? 'suspect' : 'normal'
}

/** 上游签发的 X-Codex-Turn-State 实测约 1 小时后失效。超时只提醒，不停注入。 */
export const codexTurnStateTtlMs = 60 * 60 * 1000

/**
 * 这个值发出去的时候已经过期多久，毫秒；还在时效内返回 null。
 * 注入值是我们自己挑的，挑的时候它可能早就凉了——这是不用解密、也不用猜的硬事实，
 * 和块数那种统计味的判据不是一回事。上游回带的值是响应时现签的，不适用。
 */
export function codexTurnStateExpiredByMs(
  token: FernetToken | null,
  sentAtMs: number | null,
): number | null {
  if (token === null || sentAtMs === null || !Number.isFinite(sentAtMs) || sentAtMs <= 0)
    return null
  const overdue = sentAtMs - (token.issuedAtMs + codexTurnStateTtlMs)
  return overdue > 0 ? overdue : null
}

/** 把时长渲染成 mm:ss，跨过一小时的部分再补上小时位。只取绝对值，方向由文案给。 */
export function formatCodexTurnStateDuration(ms: number): string {
  const total = Math.floor(Math.abs(ms) / 1000)
  const pad = (value: number): string => String(value).padStart(2, '0')
  const seconds = total % 60
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`
}
