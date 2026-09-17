import type { FernetToken } from '@/lib/fernet'

/** 正常形态按账号类型分两种，顺序固定，界面上按这个顺序排按钮和说明。 */
export type CodexTurnStateShape = 'individual' | 'team'
export const codexTurnStateShapeOrder = ['individual', 'team'] as const

/**
 * 实测出来的形态表。密文块数是不解密就能看到的唯一差异，所以拿它当判据：
 *   个人号正常 10 块 / 292 字符，降智 11 块 / 312 字符；
 *   team 号正常 12 块 / 332 字符，降智 13 块 / 356 字符。
 * 也就是说降智一律在各自基线上多出恰好一块，两种形态各有各的基线，不能拿一个阈值切。
 *
 * 要清楚这个判据的强度：样本不多，而且 PKCS7 填充下块数只能把明文框进一个 16 字节窗口，
 * 多一块只说明明文跨过了一次边界，不等于内容正好多了 16 字节。上游换了状态结构就要重新
 * 标定——表单独放在这里，就是为了到时候好找。
 */
export const codexTurnStateShapes: Record<
  CodexTurnStateShape,
  { blocks: number; chars: number; degradedChars: number }
> = {
  individual: { blocks: 10, chars: 292, degradedChars: 312 },
  team: { blocks: 12, chars: 332, degradedChars: 356 },
}

/** 块数命中哪一种正常形态；对不上就是 null，也就是疑似降智。 */
export function codexTurnStateShapeOf(token: FernetToken | null): CodexTurnStateShape | null {
  if (token === null) return null
  return (
    codexTurnStateShapeOrder.find((shape) => codexTurnStateShapes[shape].blocks === token.blocks) ??
    null
  )
}

/** normal 命中某种正常形态，suspect 是块数落在表外，unknown 是读不出 Fernet 结构。 */
export type CodexTurnStateVerdict = 'normal' | 'suspect' | 'unknown'

export function codexTurnStateVerdict(token: FernetToken | null): CodexTurnStateVerdict {
  if (token === null) return 'unknown'
  return codexTurnStateShapeOf(token) === null ? 'suspect' : 'normal'
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

/**
 * 这个值到「此刻」还剩多少时效，毫秒；负数表示已经过期。读不出结构就返回 null。
 * 和 codexTurnStateExpiredByMs 的差别只在参照点：那个比的是请求发出的时刻，回答「这次
 * 注入当时该不该发」；这个比的是现在，回答「这个值现在还能不能直接拿去注入」。上游回带
 * 的值只适用后者。
 */
export function codexTurnStateRemainingMs(token: FernetToken | null, nowMs: number): number | null {
  if (token === null || !Number.isFinite(nowMs)) return null
  return token.issuedAtMs + codexTurnStateTtlMs - nowMs
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
