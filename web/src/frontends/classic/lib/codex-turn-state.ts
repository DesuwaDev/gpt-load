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
