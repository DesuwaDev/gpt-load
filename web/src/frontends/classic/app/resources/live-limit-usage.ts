// 限额实时用量的轮询间隔。RPM 是 60 秒滑动窗口、并发是瞬时值，5 秒足够看出
// 变化，又不会给管理面接口带来明显压力。
export const liveLimitUsageRefetchIntervalMs = 5_000
