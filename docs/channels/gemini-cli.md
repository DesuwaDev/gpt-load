# Gemini CLI OAuth 渠道

此渠道使用 Google Code Assist（`cloudcode-pa.googleapis.com/v1internal`），支持浏览器 OAuth 和凭据文件导入。它与 AI Studio API/OAuth、Vertex AI、Antigravity 是不同的上游入口；接口转换不能增加账号的模型权限。

## 已接通的接口

| 客户端接口 | 实现 |
| --- | --- |
| `POST /v1beta/models/{model}:generateContent` | Gemini 原生请求；保留 Google Search、图片输入/输出及 grounding metadata |
| `POST /v1beta/models/{model}:streamGenerateContent` | Gemini 原生 SSE |
| `POST /v1beta/models/{model}:countTokens` | Code Assist token 计数 |
| `POST /v1/chat/completions` | Chat Completions 转换，支持普通和流式响应 |
| `POST /v1/messages` | Anthropic Messages 转换，支持普通和流式响应 |
| `POST /v1/messages/count_tokens` | Anthropic token 计数转换 |
| `POST /v1/responses` | 无状态 Responses 转换，支持普通和流式响应及 function、web_search 工具 |
| `POST /v1/responses/input_tokens` | Responses token 计数转换 |
| `POST /v1/responses/compact` | Gemini 摘要 + GPT-Load 加密封装，非流式 |
| `POST /v1/images/generations` | 图片生成转换 |
| `POST /v1/images/edits` | 图片编辑转换；支持 multipart 和 JSON data URL、多张输入图 |

模型发现继续使用渠道已有的发现/回退机制。表中的图片接口需要配置账号实际可用的生图模型，普通文本模型不会因使用图片接口而获得生图能力。

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

## 登录成功但缺少项目

如果 Code Assist 返回账号档位却没有托管项目，渠道会像 sub2api 一样调用 Cloud Resource Manager，查找此账号能访问的已有 ACTIVE 项目，优先使用 companion / Code Assist 项目，其次 default 项目。支持分页，不会自动创建项目或修改计费。没有可用项目时，页面明确提示缺少 GCP 项目，不再显示“授权码被拒绝”；可创建或加入项目后重新授权，或导入带 `project_id` 的凭据文件。
