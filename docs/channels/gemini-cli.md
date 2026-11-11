# Gemini CLI OAuth 渠道

此渠道使用 Google Code Assist（`cloudcode-pa.googleapis.com/v1internal`），支持浏览器 OAuth 和凭据文件导入。它与 AI Studio API/OAuth、Vertex AI、Antigravity 是不同的上游入口；接口转换不能增加账号的模型权限。

## 已接通的接口

| 客户端接口 | 实现 |
| --- | --- |
| `POST /v1beta/models/{model}:generateContent` | Gemini 原生请求；保留 Google Search、图片输入/输出及 grounding metadata |
| `POST /v1beta/models/{model}:streamGenerateContent` | Gemini 原生 SSE |
| `POST /v1beta/models/{model}:countTokens` | AI Studio `countTokens`；失败时按 sub2 规则估算 |
| `POST /v1/chat/completions` | Chat Completions 转换，支持普通和流式响应 |
| `POST /v1/messages` | Anthropic Messages 转换，支持普通和流式响应 |
| `POST /v1/messages/count_tokens` | Anthropic token 计数转换 |
| `POST /v1/responses` | 无状态 Responses 转换，支持普通和流式响应及 function、web_search 工具 |
| `POST /v1/responses/input_tokens` | Responses token 计数转换 |
| `POST /v1/responses/compact` | Gemini 摘要 + GPT-Load 加密封装，非流式 |
| `POST /v1/images/generations` | 图片生成转换 |
| `POST /v1/images/edits` | 图片编辑转换；支持 multipart 和 JSON data URL、多张输入图 |

模型选择与 sub2api 的 Code Assist OAuth 账号一致，返回内置可选目录，包含文本及图片模型。此列表标记为“内置模型”，不是账号权限探测；模型列表及账号卡片均不调用 `retrieveUserQuota`。表中的图片接口需要配置账号实际可用的生图模型，普通文本模型不会因使用图片接口而获得生图能力。

## 图片接口

生成请求包含 `model` 和 `prompt`。编辑请求使用相同字段，并提供以下任一形式：

- multipart：`image` 或重复的 `image[]` 文件字段。
- JSON：`images: [{"image_url": "data:image/png;base64,..."}]`，也支持单张 `image` data URL。

支持 PNG/JPEG/WebP 输入，最多 16 张；实际模型限制仍由上游执行。当前 Images 转换约定为单张输出、非流式、`n=1`、`response_format=b64_json`，`size`/`quality` 省略或为 `auto`。不支持 mask、远程 URL、上游 file ID，以及没有等价映射的图片控制项；这些输入会明确失败。上游没有返回有效图片时返回 502，不把纯文本当作生图成功。

需要直接指定 Gemini 的 `imageConfig`、`responseModalities` 等参数时，使用原生 Gemini 接口。

## 搜索

原生 Gemini 请求可使用 `tools: [{"googleSearch": {}}]`，返回完整原生搜索元数据。

Responses 请求可使用 `tools: [{"type": "web_search"}]`，也支持 preview/日期版本名称，以及 `filters.allowed_domains`；支持的模型会转换为 Google Search，结果转换为 `web_search_call` 和引用，包括 SSE。模型能力检查与底层转换器保持一致。不具有等价映射的搜索选项会报错，避免静默忽略；搜索是否执行及账号可用性仍由上游决定。

## compact 的使用方式

提交 `model`、完整的 `input` 历史及可选 `instructions`。响应对象为 `response.compaction`，`output` 保留用户/系统指令消息，并附带 `type: compaction`、`encrypted_content` 项。将整个 `output` 作为后续 Responses 的 `input`，再追加新消息；无需客户端解密。普通请求、流式请求、token 计数以及再次 compact 都支持这种续接。

这是兼容层摘要压缩，会调用一次 Gemini 并记录实际用量，不是 Google 或 OpenAI 的原生隐状态压缩。压缩项仅供本 GPT-Load 部署的 Gemini CLI 渠道使用；相同持久化加密密钥下，服务重启或同密钥实例仍可读取。更换密钥后旧项不可用，其他服务生成的 opaque compaction 不可混用。摘要被截断或为空时返回 502。

此渠道不存储上游 Responses 会话，不能仅凭 `previous_response_id` 续接，也不提供 Responses 对象的获取/删除/取消、文件/缓存对象管理、embeddings 等 Code Assist 未实现的资源接口。

## 验证范围

本地 HTTP 集成验证覆盖真实网关选择、CPA 协议转换、Code Assist 请求封装、图片上传、搜索引用、compact 往返、重建密钥服务后的续接和错误处理。上游使用本地 TLS 模拟服务，未消耗真实 Google 额度；账号的实际生图/搜索权限仍需真实上游确认。

## 与 sub2api 的 Code Assist 对齐

对照本地 `E:\ai\sub2api` 提交 `530c5e3a4`，本渠道采用以下相同行为：

| 环节 | 行为与参考代码 |
| --- | --- |
| OAuth | 内置 CLI client、`https://codeassist.google.com/authcode`、PKCE S256、离线授权、30 分钟会话；参考 `pkg/geminicli/oauth.go`、`constants.go` |
| 完成授权 | 可以粘贴 Google 显示的授权码或最终回调网址；PKCE verifier 加密存于当前授权会话，无 state 的输入只在该会话内完成 |
| 项目与档位 | `loadCodeAssist` 优先 paidTier，再 currentTier；新用户按默认/首个可用档位调用 onboardUser，最多 5 次、间隔 2 秒；已注册无项目和开通失败时查询 Resource Manager；参考 `service/gemini_oauth_service.go:fetchProjectID` |
| 请求标识 | 与参考相同的 `ANTIGRAVITY / PLATFORM_UNSPECIFIED / GEMINI` metadata 及 `GeminiCLI/0.1.5 (Windows; AMD64)` User-Agent |
| 导入与刷新 | 识别 sub2 导出的 `platform: gemini`；保留 project、oauth_type、tier_id、scope、过期时间；刷新保留已验证账号身份，不再依赖 userinfo 二次请求 |
| 模型 | Code Assist 使用同一份 17 项内置目录，含图片模型；导入的 Google One 类型使用参考的 3 项目录；参考 `handler/admin/account_handler.go`、`pkg/geminicli/models.go` |
| 请求封装 | Code Assist 使用 `{model, project, request}`；过滤空 parts，给缺少签名的 functionCall 添加同一 dummy thoughtSignature |
| 非流式生成 | 调用 `streamGenerateContent?alt=sse` 后合并候选内容、图片、工具、搜索元数据及最终 usage；参考 `service/gemini_messages_compat_service.go:ForwardNative` |
| token 计数 | 原生 AI Studio `countTokens`，失败时使用相同文本估算规则，不再调用不存在的 Code Assist countTokens 路由 |
| 额度卡片 | 档位默认 RPD/RPM + 本部署成功请求统计；参考 `service/gemini_quota.go`、`account_usage_service.go:getGeminiUsage` |

Code Assist Standard 为 1500 RPD / 120 RPM，Enterprise 为 2000 / 120；导入的 Google One Free、AI Pro、AI Ultra 分别为 1000/60、1500/120、2000/120。每日窗口按 America/Los_Angeles，分钟窗口按当前分钟。卡片标记 `(local)`：这些是本地统计与参考默认限额，不代表 Google 返回的余额，也不包含在其他客户端的消耗。统计数据缺失时显示未知，不填零冒充剩余额度。

项目发现只使用已有 ACTIVE 项目，优先 companion / Code Assist，其次 default，不会自动创建 GCP 项目或修改计费。导入凭据提供的 `project_id` 优先保留。没有可用项目时需创建或加入项目后重新授权，或导入带 `project_id` 的凭据文件。

GPT-Load 仍使用自己的加密凭据、请求统计、重试预算与故障切换设置；不会在 CPA 内叠加 sub2 的另一层自动重试。403（scope 不足除外）、429 及 5xx 交给现有网关策略处理，429 保留 Google RetryInfo 延迟；流已输出后不自动重放。sub2 的额度策略后台配置和 Google One Drive 存储档位探测不是本渠道的配置入口，当前保留导入档位并使用上述默认限额。

浏览器登录入口为 Code Assist；同时兼容导入使用内置 CLI client 的 Google One 凭据，无 project 时按参考走原生 API。AI Studio 自定义 OAuth client 与 Vertex service account 属于独立认证入口，不会把这些凭据当作 Code Assist 导入。首次登录/导入仍验证 userinfo，以满足 GPT-Load 的账号去重要求。
