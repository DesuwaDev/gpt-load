import { onScopeDispose, ref, type Ref } from 'vue'

// 轮次状态的时效读数可能同时出现在几十个凭据行和日志抽屉里，共用一个秒级时钟，
// 免得每处各起一个定时器。
const sharedNowMs = ref(Date.now())
let sharedTimer: ReturnType<typeof setInterval> | null = null
let sharedClockUsers = 0

/** 订阅共享时钟；调用方所在的 effect scope 销毁时自动退订，最后一个退订者停表。 */
export function useCodexTurnStateNow(): Ref<number> {
  sharedClockUsers += 1
  if (sharedTimer === null) {
    sharedNowMs.value = Date.now()
    sharedTimer = setInterval(() => {
      sharedNowMs.value = Date.now()
    }, 1000)
  }
  onScopeDispose(() => {
    sharedClockUsers -= 1
    if (sharedClockUsers > 0 || sharedTimer === null) return
    clearInterval(sharedTimer)
    sharedTimer = null
  })
  return sharedNowMs
}
