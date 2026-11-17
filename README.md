# APIGX

基于 New API 二次开发的 AI API 网关与模型资源共享平台。统一管理多家上游、渠道、API Key 和模型，提供模型广场、资源分享、众测与服务质量检测。

> **许可说明**：应用代码遵循 **AGPLv3**。当前仓库还内嵌了采用 **PolyForm-Noncommercial-1.0.0** 的第三方模型指纹基准数据；包含这些数据的构建受其非商业条款约束。商业使用或分发前，须移除并用自行采集、校准的数据替换，或取得相应授权。数据清单及完整许可见 [第三方许可](./new-api/THIRD-PARTY-LICENSES.md)。

## 功能

| 模块 | 能力 |
| --- | --- |
| API 网关 | 多种上游协议接入、模型映射、多 Key 轮询、错误重试与跨渠道回退 |
| 资源共享 | 分享渠道与模型供给、邀请授权、访问范围控制、供给审核 |
| 模型广场 | 按标准模型聚合来源，展示参考价格、延迟、吞吐与可靠性 |
| 服务质量 | 容量限制、用量与成本记录、Key 健康状态、短暂冷却与熔断 |
| 工具中心 | 服务商审计、Token 速度测试、八维模型掺水检测、New API 外站签到 |
| 众测 | 个人检测、选择公开结果、公共看板，以及中转站持续检测 |
| 分享中心 | Markdown 文章、投稿审核、可见性控制与“仍可用 / 已失效”投票 |

模型掺水检测使用已收录并通过对应档位校准的基准，可分别指定上游请求名与比较基准。检测会消耗上游额度；结果反映样本与基准的匹配情况，不能证明模型身份或整体能力。当前检测流程在本项目服务端运行，不依赖早期的 manxue 检测代理。

## 快速开始

### 从源码构建容器

仓库自带的 Dockerfile 会构建 APIGX 前端与 Go 后端：

```bash
git clone https://github.com/shikelea/new-apigx.git
cd new-apigx
docker build -t apigx:local ./new-api
```

创建本地 `apigx.env`，为以下两个变量填写不同的随机长字符串（例如分别运行 `openssl rand -hex 32`）：

```dotenv
SESSION_SECRET=replace_with_a_random_secret
CRYPTO_SECRET=replace_with_another_random_secret
```

以 SQLite 启动基础网关：

```bash
docker run -d --name apigx --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  --env-file apigx.env \
  -v apigx-data:/data \
  apigx:local
```

访问 `http://localhost:3000` 完成初始化并创建管理员账户。SQLite 数据保存在持久卷 `apigx-data` 中。

这是基础网关的最小启动方式。启用 APIGX 完整共享资源模式时，请结合根目录 [.env.example](./.env.example) 配置数据库、Redis、`APIGX_SECRET_KEY` 及相关安全、计量和容量开关。凭据加密主密钥应独立保存并备份，不能直接使用模板中的占位值。生产部署应配置 HTTPS 和安全 Cookie，并按 [鉴权文档](./new-api/docs/authentication.md) 设置可信 Origin。

**注意**：`new-api/docker-compose.yml` 保留了上游示例，默认拉取的是 New API 镜像；若要运行本项目，请使用上述源码构建方式，或修改 Compose 使用自行构建的 APIGX 镜像。

### 本地开发

环境要求：Go 1.25.1 或更高版本、Bun，以及项目需要的数据库。主数据库支持 SQLite、MySQL、PostgreSQL；Redis 用于缓存及共享模式相关控制。

```bash
# 终端一：前端（先构建一次，供 Go embed 使用）
cd new-api/web
bun install --frozen-lockfile
bun run build

# 终端二：后端（在仓库根目录执行）
cd new-api
go run .
```

前端热更新服务使用独立端口，避免与后端的 3000 端口冲突：

```bash
cd new-api/web
bun run dev --port 3001
```

访问 `http://localhost:3001`，开发服务默认将 API 请求代理到 `http://localhost:3000`。后端读取其工作目录下的 `.env`，配置项参考 [上游环境变量模板](./new-api/.env.example) 与 [APIGX 配置模板](./.env.example)。数据库连接参数由实际环境决定，不要直接照搬模板里的开发密码。

Windows 用户若已安装仓库内的便携工具链，可先运行：

```powershell
. .\tools\dev\env.ps1
```

该脚本只加载已有工具链；全新克隆也可以使用自己安装的 Go 和 Bun。

## 构建与测试

```bash
# 后端
cd new-api
go build ./...
go test ./apigx/... ./router ./controller ./model

# 前端
cd web
bun run typecheck
bun run test
bun run build
```

涉及数据库行为的修改需在真实 SQLite、MySQL、PostgreSQL 上回归。上述命令不代表当前版本已经完成所有环境的验证；历史验证范围和已知问题见 [开源发布说明](./docs/OPEN_SOURCE.md)。

## 仓库结构

- `new-api/`：Go 后端、React 前端与独立 `relaykit` 模块，保留上游提交历史。
- `docs/`：架构设计、开发环境、项目计划与发布说明。
- `deploy/`：部署示例、底座锁定信息与历史补丁材料。
- `tools/`：开发工具及 API、迁移、容量和路由验证脚本。

## 参与开发

提交问题时请说明版本、复现步骤、预期与实际行为，并对日志中的 Key、Cookie、Token 和个人信息脱敏。开发约定见 [后端规范](./new-api/AGENTS.md) 与 [前端规范](./new-api/web/AGENTS.md)。请勿提交 `.env`、真实凭据、运行数据库、构建产物或本地工具链。

## 许可与致谢

感谢 New API 上游及贡献者。本项目保留其 AGPLv3 许可、NOTICE 和署名，代码许可见 [LICENSE](./LICENSE)、[应用 LICENSE](./new-api/LICENSE) 与 [NOTICE](./new-api/NOTICE)。依赖和内嵌数据各自适用的许可见 [THIRD-PARTY-LICENSES.md](./new-api/THIRD-PARTY-LICENSES.md)，不可将第三方非商业数据误认为由 AGPLv3 重新授权。
