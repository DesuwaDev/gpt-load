package state

// EffectiveCredentialLimit 解析凭据限额的继承关系：凭据自己设了正值就用自己的，
// 否则回落到分组默认值。两者都为 0 表示不限。
//
// 凭据侧用 0 表达"继承"而不是"不限"，是有意的：否则把某个凭据设成 0 就能绕开
// 分组统一下发的限额，限制就不再是强制的。要让单个凭据放宽，给它一个足够大的
// 显式值即可。
func EffectiveCredentialLimit(groupLimit, credentialLimit int64) int64 {
	if credentialLimit > 0 {
		return credentialLimit
	}
	if groupLimit > 0 {
		return groupLimit
	}
	return 0
}
