/**
 * X-Codex-Turn-State 走标准 Fernet 封装，base64url 编码下的字节布局是：
 * 0x80 版本字节 + 8 字节大端秒级时间戳 + 16 字节 IV + AES-128-CBC 密文 + 32 字节 HMAC。
 * 这里只读封装本身——签发时刻与密文体积，不解密，也不需要密钥。
 */

const fernetVersion = 0x80
const fernetPrefixBytes = 1 + 8
const fernetIVBytes = 16
const fernetHMACBytes = 32
const fernetBlockBytes = 16
// 时间戳落在 2020-01-01 ~ 2100-01-01 之外的，只可能是随机字节碰巧撞上 0x80，不是签发时刻。
const fernetIssuedFloorSeconds = 1_577_836_800
const fernetIssuedCeilSeconds = 4_102_444_800

export interface FernetToken {
  /** 上游签发时刻，毫秒。 */
  issuedAtMs: number
  /** 整串解码后的字节数。 */
  totalBytes: number
  /** 密文字节数，必然是 16 的整数倍。 */
  cipherBytes: number
  /** 密文块数。 */
  blocks: number
  /**
   * PKCS7 填充下明文长度的闭区间。密文只能把明文定位到一个 16 字节窗口里，所以块数
   * 多一块只说明明文跨过了一次边界，并不等于内容正好多了 16 字节。
   */
  plaintextMinBytes: number
  plaintextMaxBytes: number
}

/** 解析 Fernet 封装；结构对不上就返回 null，调用方据此当作普通字符串处理。 */
export function parseFernetToken(value: string): FernetToken | null {
  const bytes = decodeBase64Url(value.trim())
  if (bytes === null || bytes.charCodeAt(0) !== fernetVersion) return null
  const cipherBytes = bytes.length - fernetPrefixBytes - fernetIVBytes - fernetHMACBytes
  // 空明文也要占满一个填充块，所以密文至少一块，且必然对齐。
  if (cipherBytes < fernetBlockBytes || cipherBytes % fernetBlockBytes !== 0) return null
  let seconds = 0
  for (let index = 1; index < fernetPrefixBytes; index += 1) {
    seconds = seconds * 256 + bytes.charCodeAt(index)
  }
  if (seconds < fernetIssuedFloorSeconds || seconds >= fernetIssuedCeilSeconds) return null
  return {
    issuedAtMs: seconds * 1000,
    totalBytes: bytes.length,
    cipherBytes,
    blocks: cipherBytes / fernetBlockBytes,
    plaintextMinBytes: cipherBytes - fernetBlockBytes,
    plaintextMaxBytes: cipherBytes - 1,
  }
}

/** 解出二进制串；每个字符的码位就是一个字节。补齐长度是因为 Fernet 允许省略 = 填充。 */
function decodeBase64Url(value: string): string | null {
  if (!/^[A-Za-z0-9_-]+={0,2}$/.test(value)) return null
  const body = value.replace(/=+$/, '').replace(/-/g, '+').replace(/_/g, '/')
  if (body.length % 4 === 1) return null
  const padded = body.padEnd(body.length + ((4 - (body.length % 4)) % 4), '=')
  try {
    return atob(padded)
  } catch {
    return null
  }
}
