import type { CredentialMark } from '@/api/control/types'

export type CredentialMarkTone = 'warning' | 'danger' | 'info'

/** 选项按“正常 → 降智 → 未知异常 → 自定义”排列，与徽章的告警程度一致。 */
export const credentialMarkOptions: readonly CredentialMark[] = [
  '',
  'degraded',
  'abnormal',
  'custom',
]

/** 备注上限与服务端的 maxCredentialMarkNoteRunes 保持一致。 */
export const credentialMarkNoteMaxLength = 24

const markTones: Readonly<Record<Exclude<CredentialMark, ''>, CredentialMarkTone>> = {
  degraded: 'warning',
  abnormal: 'danger',
  custom: 'info',
}

/** 未标记没有色调，调用方据此决定是否渲染徽章或圆点。 */
export function credentialMarkTone(mark: CredentialMark): CredentialMarkTone | undefined {
  return mark === '' ? undefined : markTones[mark]
}

export function credentialMarkOptionKey(mark: CredentialMark): string {
  return `group.credentials.mark.options.${mark === '' ? 'none' : mark}`
}

type Translate = (key: string) => string

/** 自定义标记没有固定名字，备注本身就是标签；其余标记把备注挂在名字后面。 */
export function credentialMarkLabel(t: Translate, mark: CredentialMark, note: string): string {
  if (mark === 'custom') return note
  const name = t(credentialMarkOptionKey(mark))
  return note === '' ? name : `${name} · ${note}`
}
