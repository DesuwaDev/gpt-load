import type { RequestLogDetailDto } from '@/app/resources/request-logs'
import { parseFernetToken } from '@/lib/fernet'

export interface TurnStateCandidate {
  value: string
  issuedAtMs: number
  requestID: string
  credentialID: number | null
  /** 凭据的可读标识（掩码），可能为空串。 */
  credentialName: string
}

/**
 * 从日志详情里挑出可用的轮次状态。
 * 只看上游回带的值——注入值是我们自己塞进去的，复制它等于把旧值再抄一遍；
 * 必须能读出 Fernet 封装。按签发时刻从新到旧排，排在最前的那条是最新签发的。
 */
export function collectTurnStateCandidates(
  logs: readonly RequestLogDetailDto[],
): TurnStateCandidate[] {
  const seen = new Set<string>()
  const candidates: TurnStateCandidate[] = []
  for (const log of logs) {
    for (const attempt of log.attempts) {
      const value = attempt.upstream_turn_state
      if (!value || seen.has(value)) continue
      const token = parseFernetToken(value)
      if (token === null) continue
      seen.add(value)
      candidates.push({
        value,
        issuedAtMs: token.issuedAtMs,
        requestID: log.request_id,
        credentialID: attempt.credential_id,
        credentialName: attempt.credential_name,
      })
    }
  }
  // 多个候选按签发时刻从新到旧排：排在最前的那条是最新签发的。
  // 同一秒内签发的多条之间没有可比的先后，保持遍历顺序（日志从新到旧）即可。
  candidates.sort((left, right) => right.issuedAtMs - left.issuedAtMs)
  return candidates
}

/**
 * 候选一共来自几个凭据。跨凭据就意味着「最新的那条」未必属于用户想注入的那个账号，
 * 这时候只能把事实摆出来，让用户用筛选把范围收窄——筛选本身就是选择器。
 */
export function countTurnStateCandidateCredentials(
  candidates: readonly TurnStateCandidate[],
): number {
  // 凭据被删掉时 credential_id 为 null，退回掩码名区分；两者都没有就算同一组。
  return new Set(candidates.map((candidate) => candidate.credentialID ?? candidate.credentialName))
    .size
}
