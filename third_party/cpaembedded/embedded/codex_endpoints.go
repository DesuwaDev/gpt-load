package embedded

import "strings"

// CodexAPIEndpoints 集中声明凭据准备完成后的业务端点，不包含 OAuth 与身份核验。
type CodexAPIEndpoints struct {
	ExecutionBase string
	AccountBase   string
}

// ResolveCodexAPIEndpoints 保留完整官方目标集合，或将原生路径映射到代理根地址。
func ResolveCodexAPIEndpoints(apiRoot string) (CodexAPIEndpoints, error) {
	endpoints := CodexAPIEndpoints{
		ExecutionBase: defaultCodexBaseURL,
		AccountBase:   defaultCodexAPIBase,
	}
	apiRoot = normalizeCodexAPIRoot(apiRoot)
	for _, endpoint := range []*string{&endpoints.ExecutionBase, &endpoints.AccountBase} {
		resolved, err := ResolveAPIEndpoint(apiRoot, *endpoint)
		if err != nil {
			return CodexAPIEndpoints{}, err
		}
		*endpoint = resolved
	}
	return endpoints, nil
}

func normalizeCodexAPIRoot(apiRoot string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(apiRoot), "/")
	if strings.HasSuffix(trimmed, "/backend-api/codex") {
		return strings.TrimRight(strings.TrimSuffix(trimmed, "/backend-api/codex"), "/")
	}
	if strings.HasSuffix(trimmed, "/backend-api") {
		return strings.TrimRight(strings.TrimSuffix(trimmed, "/backend-api"), "/")
	}
	return trimmed
}
