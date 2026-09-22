/** 自定义网关地址长度上限，与服务端的 URL 校验保持一致。 */
export const credentialBaseUrlMaxLength = 2048

/**
 * 校验自定义网关 Base URL：
 * - 空串表示跟随分组默认配置
 * - 非空时必须是合法的绝对 HTTPS 地址，不能带有认证信息、查询参数或哈希锚点
 */
export function validCodexCustomBaseURL(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return true
  try {
    const url = new URL(trimmed)
    return (
      url.protocol === 'https:' &&
      Boolean(url.hostname) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash
    )
  } catch {
    return false
  }
}
