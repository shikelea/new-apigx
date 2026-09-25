# 分享中心交付说明

分享中心是 APIGX 中独立的纯文本 Markdown 社区功能。它不创建渠道、不持有 API Key、不参与模型路由和计费，只复用底座的用户认证、权限、个人中心、后台管理和审计能力。

## 状态流转

```text
draft -> pending -> published
                  -> rejected -> draft
published -> archived
```

作者只能编辑草稿或被驳回内容；提交后必须由管理员审核。管理员可以开启“登录前不可见”，开启后匿名用户不会读取广场和详情内容。

## 可见性

- `public`：匿名用户可读（受后台登录前策略控制）。
- `login`：登录用户可读。
- `private`：仅作者可读，不能进入公开广场。

## 投票

登录用户可对已发布内容投“仍可用”或“已失效”；同一用户重复投票幂等，改投会原子迁移计数；作者不能给自己的内容投票。

## 验证

- `go test ./model ./router ./controller -count=1`
- `go test ./apigx/model -run TestMigrateMatrixOnConfiguredDialects -count=1`（已验证 SQLite 与本地 PostgreSQL；MySQL 需配置 `APIGX_TEST_MYSQL_DSN`）
- 前端 `tsgo -b`、定向 `oxlint`、`rsbuild build`
