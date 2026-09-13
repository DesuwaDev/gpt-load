# 第三方素材：ModelTrace 指纹库

`unified_bank.json` 与它所描述的归因算法来自 ModelTrace。

- 上游：<https://github.com/xqy2006/ModelTrace>
- 许可：MIT（见同目录 `LICENSE`），Copyright (c) 2026 xqy2006

本仓库只是把 `fingerprint.py` 的打分流程按位移植到 Go（见 `internal/degradation`），
没有改动指纹库内容。更新指纹库时直接覆盖本文件即可，`bank.go` 会在加载时校验维度，
维度对不上会直接报错而不是静默降级。
