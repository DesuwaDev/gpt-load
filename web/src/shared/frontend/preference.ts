export type FrontendID = 'classic' | 'modern'

/**
 * 新版界面暂时关闭。本仓库的自有功能（降智监控、凭据轮次状态、额度标记等）都只落在
 * 经典版上，新版还停在上游那套布局，进去等于少掉半个控制台。
 * 要重新开放就把这里改回 true——frontends/modern 与它的路由清单都原样留着，除此之外
 * 不需要别的改动。
 */
export const modernFrontendAvailable: boolean = false

// v2 起仅接受管理员在设置页主动写入的偏好；旧版缓存不再参与启动判断。
const frontendStorageKey = 'gpt-load.frontend.v2'
const legacyFrontendStorageKey = 'gpt-load.frontend'
const authStorageKey = 'gpt-load.auth-key'
const frontendAuthTimeoutMS = 5000

function getStorage(type: 'localStorage' | 'sessionStorage'): Storage | undefined {
  try {
    return window[type]
  } catch {
    return undefined
  }
}

function readStorageKey(type: 'localStorage' | 'sessionStorage', key: string): string {
  try {
    return getStorage(type)?.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function sessionPrincipal(response: unknown): 'admin' | 'access_key' | undefined {
  if (!isRecord(response) || response.code !== 0 || !isRecord(response.data)) return undefined
  const { authenticated, principal_type: principalType } = response.data
  if (authenticated !== true) return undefined
  return principalType === 'admin' || principalType === 'access_key' ? principalType : undefined
}

function readAuthKey(): string {
  return (
    readStorageKey('sessionStorage', authStorageKey) ||
    readStorageKey('localStorage', authStorageKey)
  )
}

function removePreference(key: string): void {
  try {
    getStorage('localStorage')?.removeItem(key)
  } catch {
    // 存储不可用时，管理员默认落回经典版入口。
  }
}

export function clearFrontendPreference(): void {
  removePreference(frontendStorageKey)
  removePreference(legacyFrontendStorageKey)
}

export async function getPreferredFrontend(): Promise<FrontendID> {
  // 旧版的全局缓存没有认证上下文，必须直接失效，避免访问密钥进入经典版。
  removePreference(legacyFrontendStorageKey)
  // 新版关闭期间所有身份一律进经典版，已经选过新版的浏览器就在这一次进站时被拉回来。
  // 顺手把偏好清掉，免得将来开关改回 true，用户又被一个早就忘掉的旧选择接管。
  // 经典版的会话同时认 admin 与 access_key，所以这条路径不会把访问密钥挡在门外。
  if (!modernFrontendAvailable) {
    clearFrontendPreference()
    return 'classic'
  }
  // 本仓库的自有功能（降智监控、凭据标记与额度等）只实现在经典版，所以管理员
  // 默认进经典版，新版改成显式选择；访问密钥身份仍按上游意图留在新版。
  if (readStorageKey('localStorage', frontendStorageKey) === 'modern') return 'modern'

  const credential = readAuthKey()
  if (!credential) return 'modern'

  const controller = new AbortController()
  let timeoutID: number | undefined
  try {
    const timeout = new Promise<undefined>((resolve) => {
      timeoutID = window.setTimeout(() => {
        resolve(undefined)
        controller.abort()
      }, frontendAuthTimeoutMS)
    })
    // 截止时间覆盖响应正文读取；迟到结果只返回身份，不再修改浏览器偏好。
    const principal = await Promise.race([
      window
        .fetch('/api/auth/session', {
          cache: 'no-store',
          headers: { Authorization: `Bearer ${credential}` },
          signal: controller.signal,
        })
        .then(async (response) => {
          if (response.status === 401) return 'unauthorized' as const
          if (!response.ok) return undefined
          return sessionPrincipal(await response.json())
        }),
      timeout,
    ])
    if (principal === 'admin') return 'classic'
    if (principal === 'unauthorized' || principal === 'access_key') {
      clearFrontendPreference()
    }
  } catch {
    // 临时网络、存储或响应异常保留管理员选择，由新版认证页提供恢复入口。
  } finally {
    if (timeoutID !== undefined) window.clearTimeout(timeoutID)
  }
  return 'modern'
}

export function switchFrontend(frontend: FrontendID): void {
  // 设置页的新版卡片已经禁用，这里再挡一道：关闭期间写进去的偏好下次启动就会被清掉，
  // 真让它写成功也只是换来一次白跑的刷新。
  if (frontend === 'modern' && !modernFrontendAvailable) {
    throw new Error('FRONTEND_NOT_AVAILABLE')
  }
  const storage = getStorage('localStorage')
  if (!storage) {
    throw new Error('FRONTEND_PREFERENCE_NOT_SAVED')
  }
  storage.removeItem(legacyFrontendStorageKey)
  if (frontend === 'modern') storage.setItem(frontendStorageKey, frontend)
  else storage.removeItem(frontendStorageKey)
  const saved = storage.getItem(frontendStorageKey)
  if ((frontend === 'modern' && saved !== frontend) || (frontend === 'classic' && saved !== null)) {
    throw new Error('FRONTEND_PREFERENCE_NOT_SAVED')
  }
  window.location.assign('/settings')
}
