import type { FernetToken } from '@/lib/fernet'

/**
 * 历史形态按账号类型曾区分个人号 (292) 与 team 号 (332)。
 * 当前上游已统一为 780 字符等更长格式，降智判定以上游返回模型一致性为准，不再以字段长度作为降智判据。
 */
export type CodexTurnStateShape = 'individual' | 'team'
export const codexTurnStateShapeOrder = ['individual', 'team'] as const

export const codexTurnStateShapes: Record<
  CodexTurnStateShape,
  { blocks: number; chars: number; degradedChars: number }
> = {
  individual: { blocks: 10, chars: 292, degradedChars: 312 },
  team: { blocks: 12, chars: 332, degradedChars: 356 },
}

/** @deprecated 上游形态已变动，请勿依赖块数判定降智。 */
export function codexTurnStateShapeOf(token: FernetToken | null): CodexTurnStateShape | null {
  if (token === null) return null
  return (
    codexTurnStateShapeOrder.find((shape) => codexTurnStateShapes[shape].blocks === token.blocks) ??
    null
  )
}

export type CodexTurnStateVerdict = 'normal' | 'suspect' | 'unknown'

/** @deprecated 上游形态已变动，请勿依赖块数判定降智。 */
export function codexTurnStateVerdict(token: FernetToken | null): CodexTurnStateVerdict {
  if (token === null) return 'unknown'
  return codexTurnStateShapeOf(token) === null ? 'suspect' : 'normal'
}
