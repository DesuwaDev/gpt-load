package control

// LiveLimitUsage 是访问密钥与凭据的本地限额实时用量来源。它读取的是网关进程
// 内的限流器计数，不落库；管理面按固定间隔轮询展示。
type LiveLimitUsage interface {
	AccessKeyUsage(accessKeyID uint) (rpmUsed, inFlight int64)
	CredentialUsage(credentialID uint) (rpmUsed, inFlight int64)
}

type accessKeyRPMUsageSource interface {
	Used(accessKeyID uint) int64
}

type accessKeyConcurrencyUsageSource interface {
	InFlight(accessKeyID uint) int64
}

type credentialUsageSource interface {
	Usage(credentialID uint) (rpmUsed, inFlight int64)
}

// NewLiveLimitUsage 组合三个限流器的只读快照。任一为 nil 时对应维度报 0。
func NewLiveLimitUsage(
	accessKeyRPM accessKeyRPMUsageSource,
	accessKeyConcurrency accessKeyConcurrencyUsageSource,
	credentials credentialUsageSource,
) LiveLimitUsage {
	return liveLimitUsage{
		accessKeyRPM: accessKeyRPM, accessKeyConcurrency: accessKeyConcurrency, credentials: credentials,
	}
}

type liveLimitUsage struct {
	accessKeyRPM         accessKeyRPMUsageSource
	accessKeyConcurrency accessKeyConcurrencyUsageSource
	credentials          credentialUsageSource
}

func (usage liveLimitUsage) AccessKeyUsage(accessKeyID uint) (int64, int64) {
	var rpmUsed, inFlight int64
	if usage.accessKeyRPM != nil {
		rpmUsed = usage.accessKeyRPM.Used(accessKeyID)
	}
	if usage.accessKeyConcurrency != nil {
		inFlight = usage.accessKeyConcurrency.InFlight(accessKeyID)
	}
	return rpmUsed, inFlight
}

func (usage liveLimitUsage) CredentialUsage(credentialID uint) (int64, int64) {
	if usage.credentials == nil {
		return 0, 0
	}
	return usage.credentials.Usage(credentialID)
}

func (s *Service) accessKeyLiveUsage(accessKeyID uint) (rpmUsed, inFlight int64) {
	if s == nil || s.limitUsage == nil {
		return 0, 0
	}
	return s.limitUsage.AccessKeyUsage(accessKeyID)
}

func (s *Service) credentialLiveUsage(credentialID uint) (rpmUsed, inFlight int64) {
	if s == nil || s.limitUsage == nil {
		return 0, 0
	}
	return s.limitUsage.CredentialUsage(credentialID)
}
