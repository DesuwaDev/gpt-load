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
