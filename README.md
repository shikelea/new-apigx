# APIGX

APIGX 是基于 [New API](./new-api/) 改造的开源 AI API 网关与模型资源共享平台，使用 APIGX 统一品牌，保留底座的 AGPLv3、NOTICE 和贡献者署名。

## 项目能力

- 兼容多种 AI API 协议，统一渠道、Key、模型和智能路由。
- 支持多 Key 轮询、错误重试、跨渠道回退、容量与质量指标。
- 模型广场按标准模型 ID 聚合不同分享来源，并展示延迟、吞吐、6 小时可靠性和参考价格。
- 分享中心提供纯 Markdown 文本分享，可发布教程、福利和渠道说明；支持公开、登录可见、私有、投稿审核及“仍可用/已失效”投票。
- 分享中心与 API 供给、Key、计费和模型路由隔离，仅复用 APIGX 的用户、认证、权限、个人中心和后台审计能力。

## 目录

- `new-api/`：APIGX 应用源码，已作为 tracked subtree 纳入本仓库并保留完整提交历史。
- `deploy/`：本地与部署配置、底座锁定信息和历史补丁材料。
- `docs/`：架构、开发环境和项目计划。
- `whisper-note-dev-flow/`：连续开发台账、任务记录和代码风格约定。
- `tools/`：本地 Go、PostgreSQL、Redis 和网络辅助脚本。

## 本地开发

Windows 环境先加载项目工具链：

```powershell
. .\tools\dev\env.ps1
```

后端：

```powershell
cd new-api
..\.toolchain\go\bin\go.exe test ./model ./router ./controller -count=1
..\.toolchain\go\bin\go.exe run .
```

前端：

```powershell
cd new-api\web
npm install
npm run dev
```

生产构建：

```powershell
cd new-api\web
npm run build
```

默认数据库可使用 SQLite；生产建议使用 MySQL 8 或 PostgreSQL，并按 `new-api/.env.example` 配置 `SQL_DSN`。分享中心表会随底座启动迁移自动创建：`share_posts` 和 `share_post_votes`。

## 分享中心接口

公开阅读：`GET /api/share-center`、`GET /api/share-center/:publicID`。

登录用户：`GET /api/share-center/mine`、`POST /api/share-center`、`PUT /api/share-center/:publicID`、`POST /api/share-center/:publicID/submit`、`DELETE /api/share-center/:publicID`、`POST /api/share-center/:publicID/vote`。

管理员审核与策略：`GET /api/share-center/admin/posts`、`POST /api/share-center/admin/posts/:publicID/approve`、`POST /api/share-center/admin/posts/:publicID/reject`、`GET/PUT /api/share-center/admin/config`。

Markdown 由前端已有安全渲染组件解析并通过 DOMPurify 清理；后端只保存 Markdown 文本，不执行用户提交的 HTML 或脚本。

## 许可

本项目基于 New API 二次开发，遵循 AGPLv3。发布修改版时请同时提供对应源码，并保留 `new-api/LICENSE`、`new-api/NOTICE` 和上游署名要求。
