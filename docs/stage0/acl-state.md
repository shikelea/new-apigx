# APIGX 授权判定与资源生命周期状态流

阶段 0 产出（设计冻结件）。本文把「谁在什么条件下能调用什么、暂停与撤权之后已接收的请求怎么办」落成**无歧义**的状态流。

**输入依据**

| 来源 | 用途 |
|---|---|
| `docs/PROJECT_PLAN.md` v1.0 §2、§3.5、§7、§9.1、§9.2、§9.5、§10.1、§10.2、**§10.3**、§12 | 对象模型、硬限制/偏好、错误码、邀请、生命周期表、保留期基线 |
| `docs/stage0/base-lock.md` §6 | 底座强制规范（含「认证安全按 OWASP ASVS 执行」） |
| `whisper-note-dev-flow/CODE_STYLE.md` 第 13–24 条 | 工具链、底座硬约束、文档落位 |
| 底座源码 `new-api/` @ `69a50029819a26c53e6babd276d49cfe2f8880ad` | 落地依据（只读分析） |

**结论强度声明（务必遵守）**

1. 本文全部结论是**源码阅读 + 设计推导**层面的。**没有**编译过底座、**没有**运行过任何测试、**没有**部署、**没有**做过安全验证或性能验证。
2. `new-api/` 在本轮工作中**未被修改**（只读分析）。
3. 引用一律按「`文件:行号`」（相对 `new-api/`）。未直接读到或未穷尽证明的写**未核实**，不做猜测。
4. 本文的所有规则都是**确定性**的：不出现「视情况」「看实现」。凡是需要分支的地方，分支条件必须是**已存储的显式字段**（例如紧急封禁的 `scope`），而不是运行时的模糊判断。

---

## 0. 约定

### 0.1 稳定 ID

授权与调用**只用稳定 ID**，名称仅作展示（计划书 §2、§3.5；CODE_STYLE 第 1 条）。

| 对象 | ID | 说明 |
|---|---|---|
| 账号 | `account_id` | 平台用户 |
| 平台 Key | `key_id` | 使用者接入凭据；一个账号可有多把 |
| 分享分组 | `group_id` | 分享者自定义集合 |
| 渠道 | `channel_id` | 一个渠道属于一个分组 |
| 模型供给 | `offering_id` | 渠道 × 标准模型 |
| 上游凭据 | `upstream_key_id` + `credential_version` | 渠道内 Key 池成员及其版本 |
| 授权 | `grant_id` | 见 §3.1 |
| 邀请 | `invite_id` | 见 §3.2 |
| 请求 / 尝试 / 任务 | `request_id` / `attempt_id` / `task_id` | 请求与尝试分离 |
| 既有对象 | `object_ref_id` | 文件、会话、缓存、任务产物引用 |

### 0.2 状态枚举（本文的权威定义）

枚举值前带 `*` 的是**相对底座需要新增**的，不带的是**底座已有或可映射**。

```text
AccountState        : active | banned | deleted
PlatformKeyState    : active* | revoked | expired | quota_exhausted | retired*
ShareGroupVis       : public | restricted* | private*
ShareGroupState     : active | suspended* | delisted* | retired
OfferingVis         : public | restricted* | private*
OfferingState       : active | suspended* | delisted* | retired
GrantKind           : group | offering*
GrantState          : active | expired | revoked
InviteKind          : registration* | resource*
InviteState         : active | exhausted* | expired | revoked
UpstreamKeyState    : enabled | disabled | cooling* | expired* | quota_exhausted*
                    | retired* | revoked_emergency*
ChannelState        : enabled | paused* | auto_disabled | retired*
EmergencyScope      : stop_outbound* | hide_results* | deny_identity*
```

**与底座既有枚举的映射**（依据：`common/constants.go:237-258`）

| 底座 | 值 | 本文映射 |
|---|---|---|
| `UserStatusEnabled` / `UserStatusDisabled` | 1 / 2 | `AccountState.active` / `AccountState.banned` |
| `TokenStatusEnabled` / `Disabled` / `Expired` / `Exhausted` | 1 / 2 / 3 / 4 | `PlatformKeyState.active` / `revoked` / `expired` / `quota_exhausted` |
| `ChannelStatusUnknown` / `Enabled` / `ManuallyDisabled` / `AutoDisabled` | 0 / 1 / 2 / 3 | — / `enabled` / `paused` / `auto_disabled` |

**可见性严格序**（`rank(public)=0 < rank(restricted)=1 < rank(private)=2`）：

```text
effective_visibility(offering) = max( rank(group.visibility), rank(offering.visibility) )   # 取更严者
```

### 0.3 三条基础原则

- **P1 仅收窄（monotonic narrowing）**：任何一层授权只能收窄整体结果。层与层之间是 **AND（交集）**，不存在「某一层放宽以补偿另一层拒绝」的路径。
- **P2 可见与可调用分离**（计划书 §2）：*可见* = 该对象是否出现在该用户的任何展示面（广场、搜索、计数、详情、`/v1/models`）；*可调用* = 除可见外，还要求状态、健康、容量、额度全部通过。可见 ⇒ 未必可调用；不可调用 ⇏ 不可见（例如「公开但暂停」仍可见、标注不可用）。
- **P3 所有权与身份分离**：既有对象的读取授权来自**创建时形成的账号所有权**（快照，不可转移）；当前账号/凭据状态**始终实时求值**。二者是 AND（计划书 §10.3 表后段落）。

---

## 1. 有效权限判定顺序

### 1.1 判定流程（确定性步骤）

判定输入：`凭据 + 请求头 + 请求体 + 目标对象`。每一步的拒绝都**立即终止**，不继续后续步骤。

| 步 | 门 | 判定内容 | 拒绝结果 |
|---|---|---|---|
| S0 | 输入规范化 | 解析凭据与请求头；平台扩展头标记为「出站前剥除」 | 400 `PARAM_INVALID` |
| S1 | 凭据认证 | 按平台 Key 摘要查找；`state == active`；未过期；额度未耗尽 | 401 `CREDENTIAL_INVALID` |
| S2 | 账号状态 | `account.state == active` | 401 `ACCOUNT_DISABLED` |
| S3 | Key 硬限制（模型） | 请求模型 ∈ `key.allowed_models`（`ModelLimitsEnabled == false` 表示「明确不限」，见 §1.3） | 403→**对外 404**（见 §7.2） |
| S4 | Key 硬限制（来源） | 候选供给集合 ⊆ `key.allowed_offerings` | 400 / 404（见 §1.4） |
| S5 | 固定来源 | `key.pinned_offering != null` ⇒ 目标必须等于该供给 | 400 `PARAM_CONFLICT` |
| S6 | 参数冲突检查 | `X-APIGX-Offering` 与 `X-APIGX-Route` 同时出现；固定来源 Key 携带 `X-APIGX-Route` | 400 `PARAM_CONFLICT` |
| S7 | 参数收敛 | 请求参数只能从 S3–S5 的集合中**选择**，不得新增成员 | 404（不可见）或 400（冲突） |
| S8 | 分组访问策略 | `group.state == active` ∧ `effective_visibility` 门槛满足 | 404 |
| S9 | 供给访问策略 | `offering.state == active` ∧ 供给层门槛满足 | 404（不可见）/ 503（可见但不可调） |
| S10 | Grant 叠加 | 若 effective 门槛为 `restricted` ⇒ 必须存在 `GrantKind ∈ {group, offering}`、`state == active`、未过期的 Grant 且 scope 命中 | 404 |
| S11 | 健康与容量 | 授权候选内存在健康渠道 ∧ 渠道/上游 Key/共享限额池/并发/RPM/TPM/预算全部可占用 | 429（有候选但满载）/ 503（无候选） |
| S12 | 偏好选路 | 依 Key 默认偏好或合法请求参数排序，选出**唯一权威**供给与凭据引用 | 503 |
| S13 | 出站前重验 | `AuthorizeForOutbound`（§6.3），写入出站尝试记录后发送 | 见 §6.3 |

**S3–S10 是「授权交集」，S11 是「额度与容量约束」，S12 只在 S1–S11 的幸存集合内排序。** 排序永远不能改变授权结论（计划书 §9.2「质量再高也不能绕过权限」）。

### 1.2 伪代码

```text
function authorize_request(credential, headers, body) -> Decision:

    # ---- S1 凭据 ----
    key = lookup_platform_key(digest(credential))          # 仅按不可恢复摘要查找（§7.5）
    if key == null or key.state != active:  return DENY(401, CREDENTIAL_INVALID)
    if key.expires_at != null and key.expires_at <= now_utc:
                                            return DENY(401, CREDENTIAL_INVALID)
    if not key.unlimited_quota and key.remaining_quota <= 0:
                                            return DENY(401, CREDENTIAL_INVALID)

    # ---- S2 账号 ----
    account = load_account(key.account_id)
    if account.state != active:             return DENY(401, ACCOUNT_DISABLED)

    # ---- S6/S7 参数冲突（先于可见性，避免用错误码区分对象是否存在）----
    want_offering = headers["X-APIGX-Offering"]            # 可为空
    want_route    = headers["X-APIGX-Route"]               # 可为空
    if want_offering != null and want_route != null:
                                            return DENY(400, PARAM_CONFLICT)
    if key.pinned_offering != null and want_route != null:
                                            return DENY(400, PARAM_CONFLICT)
    if key.pinned_offering != null and want_offering != null
                                    and want_offering != key.pinned_offering:
                                            return DENY(400, PARAM_CONFLICT)

    # ---- S3/S4 从 Key 硬限制出发构造候选（Key 是授权的下界，不是上界的可协商项）----
    allowed_models    = key.allowed_models                 # 显式集合；见 §1.3「明确不限」
    allowed_offerings = key.pinned_offering != null
                        ? { key.pinned_offering }
                        : key.allowed_offerings

    if want_offering != null:
        if want_offering not in allowed_offerings:  return DENY(400, PARAM_CONFLICT)
        target_offerings = { want_offering }
    else:
        target_offerings = allowed_offerings               # 不因省略参数而扩大

    requested_model = resolve_canonical_model(body.model)  # 歧义 ⇒ DENY(400, PARAM_INVALID)
    if not requested_model in allowed_models:   return DENY(404, RESOURCE_NOT_ACCESSIBLE)

    candidates = []
    for o in target_offerings:
        if o.canonical_model != requested_model.version_exact:  continue   # 严格同版本（§8.2）
        cand = evaluate_layer_visibility_and_state(account, key, o)        # ---- S8/S9/S10 ----
        if cand.decision != ALLOW:  continue
        candidates.append(cand)

    if candidates.is_empty():                   return DENY(404, RESOURCE_NOT_ACCESSIBLE)

    # ---- S11 健康与容量 ----
    healthy = [c for c in candidates if c.has_healthy_channel]            # 暂停/下架/无合格 Key 者出局
    if healthy.is_empty():
        # 判别：是否存在「可见但当前不可调用」的候选
        return any(c.visibility_kept for c in candidates)
               ? DENY(503, NO_HEALTHY_TARGET)
               : DENY(404, RESOURCE_NOT_ACCESSIBLE)

    routable = [c for c in healthy if capacity_probe(c).available]        # 原子预留前的可预留判定
    if routable.is_empty():                     return DENY(429, CAPACITY_EXCEEDED)

    # ---- S12 偏好选路：只在 routable 内排序，绝不扩集 ----
    mode = want_offering != null           ? PINNED
         : want_route   != null            ? want_route
         : key.default_route_mode != null  ? key.default_route_mode
         : AUTO                                                    # 系统默认（计划书 §9.1）
    if key.pinned_offering != null:  mode = PINNED                 # 固定来源覆盖偏好，不可被参数改回
    chosen = order_and_select(routable, mode)

    # ---- S11 尾段：原子预留（任一维度失败 ⇒ 撤销本次全部未生效预留并重选/拒绝）----
    if not reserve_atomically(account, key, chosen, requested_model):
                                            return DENY(429, CAPACITY_EXCEEDED)

    return ALLOW(chosen)         # 唯一权威路由决定（计划书 §6 架构约束）
```

```text
function evaluate_layer_visibility_and_state(account, key, offering) -> LayerDecision:

    group = load_group(offering.group_id)

    # 所有权路径：所有者可管理自己的资源，但调用同样计量与限额（计划书 §7）
    is_owner = (group.owner_account_id == account.account_id)

    # 1) 状态门（所有者不豁免：暂停/下架的语义就是「不接新调用」）
    if group.state    != active:  return DENY_404_if_hidden(group)      # suspended=可见/503，delisted=404
    if offering.state != active:  return DENY_404_if_hidden(offering)

    # 2) 可见性门：private 只能由所有者通过；Grant 不能覆盖 private
    eff = max(rank(group.visibility), rank(offering.visibility))
    if eff == private and not is_owner:                 return DENY(404)
    if eff == private and is_owner:                     return ALLOW(visibility_kept=YES)

    # 3) restricted 门：需要 active 且未过期的 Grant（所有者不需要，但也不叠加额外权限）
    if eff == restricted and not is_owner:
        if not exists_grant(account, scope in {group, offering}, state=active, expires_at > now_utc):
            return DENY(404)

    # 4) 可信分享者范围（若 Key 配置了 allowed_sharers，则必须命中）
    if key.allowed_sharers != null and group.owner_account_id not in key.allowed_sharers:
        return DENY(404)

    return ALLOW(visibility_kept = (eff != private), layer_grant = ...)
```

### 1.3 硬限制 vs 默认偏好

| 维度 | 类别 | 存在位置 | 请求参数能否改变 | 规则号 |
|---|---|---|---|---|
| 允许模型集合 | **硬限制** | 平台 Key（`allowed_models`） | **只能收窄**，不能放宽 | R-HARD-01 |
| 允许来源集合 | **硬限制** | 平台 Key（`allowed_offerings`） | **只能收窄** | R-HARD-02 |
| 固定来源 | **硬限制** | 平台 Key（`pinned_offering`） | **不可解除、不可改写** | R-HARD-03 |
| 是否允许回退 | **硬限制** | 平台 Key（`allow_fallback`），默认 `false` | **不可放宽** | R-HARD-04 |
| 可信分享者范围 | **硬限制** | 平台 Key（`allowed_sharers`） | 只能收窄 | R-HARD-05 |
| 允许协议 / 能力 | **硬限制** | 平台 Key + 供给能力声明 | 只能收窄 | R-HARD-06 |
| 默认模型 | 偏好 | 平台 Key | 可被请求体覆盖 | R-PREF-01 |
| 默认路由模式 | 偏好 | 平台 Key（`default_route_mode`） | 可被合法参数覆盖；固定来源时被 `PINNED` 覆盖 | R-PREF-02 |
| 渠道内 Key 池策略 | 分享者偏好 | 渠道 | 使用者不可见、不可改（计划书 §3.5） | R-PREF-03 |

**显式表达要求**（CODE_STYLE 第 11 条、计划书 §9.2）：`未配置 / 禁用 / 继承默认 / 明确不限` 必须分别存储。

- R-HARD-01a：`allowed_models` 的「明确不限」写作 `allowed_models = UNRESTRICTED`（显式哨兵），**不得**用空集合或 NULL 表达「不限」；空集合表示「不允许任何模型」。
- R-HARD-02a：`allowed_offerings` 同理；`pinned_offering` 与 `allowed_offerings` 同时有值时，以 `{pinned_offering}` 为准（交集语义的严格收敛）。
- R-HARD-04a：`allow_fallback` 为 `true` 时，回退目标必须 ⊆ `allowed_offerings`，且回退同样需要满足 S8–S11（回退不是旁路）。

### 1.4 R-PARAM-01：请求参数不能扩大授权（形式化）

对任意维度 `D`（模型、来源、分享者、协议、能力），定义：

```text
effective_set(D, request) ⊆ authorized_set(D, key)         # 恒成立，无例外
```

由此推出三条必须实现的硬规则：

- **R-PARAM-01a（模型）**：`body.model` 只是 `allowed_models` 的选择器。选择器命中失败 ⇒ 不进入候选，按 §7.2 返回。
- **R-PARAM-01b（来源）**：`X-APIGX-Offering` 只是 `allowed_offerings` 的选择器。
  - 该 id **不在** `allowed_offerings` ⇒ 400 `PARAM_CONFLICT`（无论该供给是否存在，因此不构成枚举通道）。
  - 该 id **在** `allowed_offerings` 但当前不可见（S8/S9/S10 拒绝）⇒ 404。
- **R-PARAM-01c（策略）**：`X-APIGX-Route` 只改变 §12 的**排序函数**，不改变候选集合；固定来源 Key 携带该头 ⇒ 400（R-HARD-03）。
- **R-PARAM-01d（请求体 group 等旁路字段）**：任何客户端可提交的分组/渠道/来源字段都必须先经 `effective_set` 检查再使用；**底座现状是这类字段存在旁路**，见 §1.5。

### 1.5 与底座现状的差距（必须修）

| 现状 | 位置 | 问题 | 本文要求的收敛 |
|---|---|---|---|
| 渠道管理整组挂 `AdminAuth()` | `router/channel-router.go:19-37` | 普通分享者无法拥有自己的渠道 | 新增 owner 约束的分享者接口，不开放现有管理路由 |
| 渠道实体的 40+ 条管理路由按 `authz.Permission` 分权 | `router/channel-router.go:39-82` | 权限模型面向管理员 | 分享者接口独立注册，不复用该组 |
| 渠道结构**没有 owner 字段** | `model/channel.go:23-60`（字段清单内无 owner/owner_user_id） | 无法表达「谁的渠道」 | 新增 `owner_account_id` 与分享分组归属（计划书 §5.2、§7） |
| **指定渠道（pin）分支跳过 token 模型限制** | `middleware/distributor.go:48-78`（pin 分支只有渠道存在/启用/过滤器校验）对比 `middleware/distributor.go:79-99`（非 pin 分支才做 `token_model_limit` 校验） | 指定来源可绕过「允许模型」硬限制 | 把 S3–S10 收敛为**单一授权函数**，pin 与非 pin 走同一路径（计划书 §5.2 第 1 条） |
| 指定渠道仅限管理员 | `middleware/auth.go:536-553`（普通用户返回 403 `普通用户不支持指定渠道`） | 普通用户无法指定来源 | 改为基于 `allowed_offerings` 授权，而非角色 |
| 分组可用性依赖底座用户分组 | `middleware/auth.go:455-471`、`service/group.go:14-43`、`setting/ratio_setting/group_ratio.go:66` | 底座「用户分组」≠ 分享分组（计划书 §2 边界 1） | 分享分组独立成表，绝不塞进现有用户分组字段（CODE_STYLE 第 9 条） |
| 亲和缓存命中后直接选渠道 | `middleware/distributor.go:126` | 缓存命中可能绕过当前授权 | 命中后仍执行完整 S3–S11；资源修订使亲和失效（§6.5） |
| 任务提交走原渠道 pin | `relay/relay_task.go:174`、`dto/channel_constraints.go:5-8,71-117` | 与 Web 走不同路径 | 与 Web 收敛到同一授权函数（§6.4） |

> 未核实：`service/authz` 的 `Permission` 语义、Casbin 策略定义、`RequirePermission`/`SecureVerificationRequired`/`CriticalRateLimit` 的内部实现**未读取**。上表只断言「路由注册与中间件挂载」这一层的事实。

---

## 2. Grant 与两类邀请

### 2.1 Grant 模型

```text
Grant {
  grant_id              # 稳定 ID
  subject_account_id    # 被授权账号
  kind                  # group | offering
  scope_id              # group_id 或 offering_id
  origin_invite_id      # 可空；手工指定的 Grant 为 NULL
  granted_by_account_id # 分享者（或管理员按策略）
  created_at            # UTC
  expires_at            # 可空 = 不过期；UTC
  state                 # active | expired | revoked
  revoked_at, revoke_reason
}
```

**规则**

- **R-GRANT-01**：一个 Grant 只覆盖**一个** scope。需要多 scope ⇒ 多条 Grant；**禁止**用一个 Grant 表达多个不相交对象。
- **R-GRANT-02**：Grant 只解决 `restricted` 门槛。**Grant 不能覆盖 `private`，也不能覆盖 `state != active`**（§0.3 P1、§1.2 第 2 步）。
- **R-GRANT-03**：`expires_at` 到期与 `revoked` 都是终局；不自动续期。到期影响后续调用，**不影响已生成对象**（§5 行 R1c）。
- **R-GRANT-04**：所有者路径不需要 Grant；但所有者调用**同样计量、同样受限额**（计划书 §7）。
- **R-GRANT-05**：Grant 的判定读 `state` 与 `expires_at` 的当前值（实时），缓存规则见 §6.2。
- **R-GRANT-06**：模型合并、别名调整、分组更名**不改变**已有 Grant 与历史请求归属（计划书 §7）。

### 2.2 两类邀请：类型与校验逻辑必须独立

计划书 §10.1 明文要求「注册邀请码与资源访问邀请码使用独立类型和校验逻辑」。

| 维度 | 注册邀请 `registration` | 资源邀请 `resource` |
|---|---|---|
| 授予物 | **仅**「创建账号」的资格 | 一个明确 scope 的访问权（产出 Grant） |
| 是否授予资源权 | **否**（永不） | 是（仅 scope 内） |
| 是否授予注册权 | 是 | **否**（未注册者必须先完成注册） |
| 校验函数 | `VerifyRegistrationInvite` | `VerifyResourceInvite` |
| 兑换端点 | `POST /api/user/register`（服务端在创建账号前校验） | `POST /api/invites/redeem` |
| 目标范围字段 | 无（或仅注册策略标签） | `scope_kind ∈ {group, offering}` + `scope_id`（必填） |
| 与邮箱验证的关系 | **AND 组合**，可同时要求，不是互斥的注册方式（计划书 §10.1） | 无关 |
| 产出对象 | 无 Grant；账号上记录 `registration_invite_id` 供审计追溯 | 1 条 Grant（`origin_invite_id = invite_id`） |
| 到期语义 | 影响后续注册 | 影响后续兑换；已生成 Grant 有**独立**到期时间（计划书 §7） |
| 撤销语义 | 不撤销已注册账号（注册是不可逆事件），且**不存在**需要级联的资源 Grant | 见 §2.4 |
| 管理权限 | 管理员（策略配置） | 资源所有者（scope 内）+ 管理员按策略 |

**R-INV-01（命名空间隔离）**：两种邀请的凭据明文与摘要**必须域分离**：

```text
code_hash = HMAC(key = K_invite[kind], message = kind || "\x00" || code)
```

`kind` 参与摘要域 ⇒ 一个 `registration` 邀请码**在任何情况下都不可能**在 `resource` 校验路径中命中，反之亦然。**禁止**出现不带 `kind` 的 `WHERE code_hash = ?` 查询。

**R-INV-02（独立存储域）**：即使选择同表，`kind` 必须参与全部唯一约束与索引；两种类型的列表/统计/撤销接口不得联合查询。

**R-INV-03（不隐式提权）**：邀请码仅赋予其声明范围内的能力，**不能隐式授予管理员权限**（计划书 §10.1）。

**R-INV-04（兑换前置条件）**：`resource` 兑换要求兑换者 `AccountState == active`；`registration` 兑换在注册策略允许时由注册流程承担。

**R-INV-05（兑换页不泄露）**：邀请兑换页在兑换成功前**不暴露受限资源详情**（计划书 §2）——兑换前只返回「这是一个资源邀请」与（仅在 scope 为 `public`/`restricted` 时）所有者展示名，不返回供给列表、不返回同组其他供给。

### 2.3 原子核销、到期、撤销

**R-INV-10（原子扣减，唯一权威写法）**

```sql
-- 单条语句完成「校验 + 扣减」，禁止先 SELECT 后 UPDATE
UPDATE invite
   SET redeemed_count = redeemed_count + 1,
       state = CASE WHEN redeemed_count + 1 >= max_uses THEN 'exhausted' ELSE state END,
       last_redeemed_at = :now_utc
 WHERE id = :invite_id
   AND kind = :kind
   AND state = 'active'
   AND redeemed_count < max_uses
   AND (expires_at IS NULL OR expires_at > :now_utc);
```

- `affected_rows == 0` ⇒ **拒绝**，统一错误码 `ERR_INVITE_UNAVAILABLE`（**不区分**不存在 / 已用尽 / 已过期 / 已撤销，避免枚举）。
- `affected_rows == 1` ⇒ 在**同一数据库事务**内创建 Grant（`resource`）或写入账号的 `registration_invite_id`（`registration`）。事务提交失败则扣减一并回滚。
- **R-INV-10a（并发）**：并发兑换由数据库行级写锁与 `state`/`redeemed_count` 条件共同保证；不使用应用层锁作为正确性依赖。
- **R-INV-10b（幂等）**：幂等键为 `(invite_id, subject_account_id)`，唯一索引 `ux_grant_invite_subject`（`resource`）承担最终判定。重复兑换 ⇒ 不重复扣减、返回既有 `grant_id`。
- **R-INV-10c（类型不互认）**：幂等键与唯一约束**都包含 `kind`**。

**R-INV-11（到期）**

- `expires_at <= now_utc` ⇒ 兑换失败（`ERR_INVITE_UNAVAILABLE`）；`state` 由惰性求值 + 定期任务落库为 `expired`。
- 到期**不**影响在此之前已经生成的 Grant（R-GRANT-03）。
- 邀请的默认有效期与最大次数在创建时**必须显式给定**（不设无限期默认）；`max_uses >= 1`。

**R-INV-12（撤销）**

```text
POST /api/invites/{invite_id}/revoke
body: { "cascade": "none" | "generated" }        # 必填，无默认值
```

- `cascade` **缺失或非法** ⇒ 400 `PARAM_INVALID`。这是刻意的：**不允许**由服务端猜测级联意图。
- 撤销本身在单事务内完成：`invite.state = revoked` +（当 `cascade == generated` 时）级联更新。
- 撤销**只影响后续兑换**；已生成 Grant 的处置完全由 `cascade` 决定（§2.4）。
- 撤销是审计事件，记录 `invite_id / kind / cascade / affected_grant_count / actor / at`，**不含邀请码明文**（§7.5）。

### 2.4 R-INV-13：撤销邀请是否连带撤销由它产生的 Grant（确定语义）

**这是确定的二选一，不存在第三种行为。**

| `cascade` | 对 `invite` | 对 `origin_invite_id == invite_id` 的 Grant | 对 `origin_invite_id IS NULL` 的手工 Grant |
|---|---|---|---|
| `none` | `state = revoked` | **不变**（保持 `active`，直到自身到期或被单独撤销） | **不变** |
| `generated` | `state = revoked` | 同事务内 `state = revoked, revoke_reason = invite_revoked_cascade`；仅处理当前 `state == active` 的行 | **不变**（结构上不可能命中） |

```sql
-- cascade = generated 时的级联语句（与 invite 更新同事务）
UPDATE grant
   SET state = 'revoked', revoked_at = :now_utc, revoke_reason = 'invite_revoked_cascade'
 WHERE origin_invite_id = :invite_id
   AND state = 'active';
```

补充确定规则：

- **R-INV-13a**：`cascade = none` 之后**再**执行 `generated` 撤销同一邀请——**不允许**（邀请已 `revoked`，二次撤销返回 409/400 类确定性拒绝，不重复级联）。级联只能随撤销一次性发生。
- **R-INV-13b**：撤销单条 Grant **不**改动邀请（反向不成立）。
- **R-INV-13c**：级联撤销后的既有对象行为按 §5 行 **R1c** 处置（历史任务在保留期内由原用户读取）。
- **R-INV-13d**：级联撤销**不**删除 Grant 行，只改状态（软删除，保留被请求/任务/计量记录引用的稳定 ID，CODE_STYLE 第 2 条）。
- **R-INV-13e**：`registration` 邀请的撤销**永不**级联（不存在产物），已注册账号与其后续获得的资源 Grant 不受影响。

### 2.5 邀请码的存储与安全（ASVS）

| 规则 | 内容 | ASVS 5.0 |
|---|---|---|
| R-INV-20 | 邀请码仅**创建时展示一次**；持久层只保存 `code_hash`（HMAC，见 R-INV-01），不保存可恢复明文 | 6.5.2（<112 bit 熵须用带 32 位随机盐的密码存储算法；本文采用域分离 HMAC + 高熵码） |
| R-INV-21 | 邀请码由 CSPRNG 生成，熵 ≥ 112 bit（例如 20+ 随机字符） | 6.5.3、6.5.4 |
| R-INV-22 | 每个码有确定有效期与使用次数上限 | 6.5.5、6.4.1 |
| R-INV-23 | 任何邀请码（含一次性）成功兑换后不可重放；重复兑换按 R-INV-10b 返回既有结果 | 6.5.1、6.6.2 |
| R-INV-24 | 邀请（一种认证因子）可被撤销，撤销立即生效于后续兑换 | 6.5.6 |
| R-INV-25 | 兑换端点限流 + 统一错误（不区分不存在/耗尽/过期/撤销），防暴力与枚举 | 6.3.1、6.3.8（注册与找回流程同样适用）、6.6.3、16.3.3 |
| R-INV-26 | 所有校验在**服务端**执行；前端校验不作为依据 | 底座 AGENTS.md 认证安全条款；6.3.4（多认证路径不得有未记录旁路，且强度一致） |

### 2.6 与底座现状的差距（必须新建，不得复用命名）

| 现状 | 位置 | 结论 |
|---|---|---|
| 注册只有开关，**没有注册邀请码准入** | `common/constants.go:64-71`（`RegisterEnabled` / `PasswordRegisterEnabled` / `EmailVerificationEnabled`） | 注册邀请策略需**新建** |
| `aff_code` 只是推广归因，不构成准入 | `controller/user.go:272-273`：`affCode := user.AffCode` 后 `inviterId, _ := model.GetUserIdByAffCode(affCode)` —— **错误被丢弃**，缺失或无效邀请码不阻止注册 | **禁止**复用 `aff_code` 名称宣称邀请功能完成（计划书 §5.2 第 2 条） |
| 邮箱验证码的实现形态 | `common/verification.go:16-56`：`purpose` 前缀区分用途、`VerificationValidMinutes = 10`、存放在**进程内 map** | 可参考其 purpose 分离思路；但邀请需持久化、需并发原子扣减，**不能**照搬内存 map |
| 平台 Key 以**可恢复明文**存储并按明文查询 | `model/token.go:17`（`Key` 列）、`model/token.go:290`（`DB.Where(commonKeyCol+" = ?", key)`） | 与计划书 §10.3「只存不可恢复校验摘要」冲突，需改为摘要索引（本项为 §7.5 的直接来源） |
| 注册流程服务端校验已存在 | `controller/user.go:216-223`（开关）、`:240-248`（邮箱验证码 + `VerifyCodeWithKey`）、`:262-270`（重名/重邮箱） | 复用其服务端强制形态 |

> 未核实：以对 `*.go` 检索 `InviteCode|invite_code|InviteEnabled` 未命中为依据，判定「底座在本次检索范围内不存在注册邀请码机制」。这是**检索级结论**，不是全仓穷尽证明（未读取前端、迁移脚本与插件目录的全量内容）。

---

## 3. 单供给邀请的边界

### 3.1 R-SCOPE-01：授权范围 = 目标供给本身

`resource` 邀请的 `scope_kind = offering` 时：

- 产出的 Grant：`kind = offering`，`scope_id = target_offering_id`，且 **`allowed_offerings = { target_offering_id }`**（精确单元素集合）。
- **不授予**同组其他供给的可见性或可调用性。
- 可见性计算、搜索结果、来源计数、模型详情的来源列表、`/v1/models` 输出，**全部按 offering 粒度**计算：只包含 `target_offering_id`。
- 同一分组内其他供给对兑换者**仍为不可见**，其存在性不得通过以下任何渠道泄露：计数、排序、错误码、耗时、结果条数（§7.4）。

### 3.2 R-SCOPE-02：展示所属分组所必需的**最小信息**

为让兑换者知道「这是谁分享的哪些资源」，允许展示的对象**仅限**：

| 允许 | 禁止 |
|---|---|
| 分组的 `group_id`（稳定 ID，调用与授权用） | 分组内其他**任何** `offering_id`、模型名、上游模型 ID |
| 分组的展示名 `display_name` | 分组内其他供给的数量（用于推断规模） |
| 分组所有者的展示名（公开昵称） | 分组内其他供给的价格、质量、可用性 |
| 目标供给自身的展示信息 | 渠道 Base URL、协议、上游 Key 名称或 ID |

- **R-SCOPE-02a**：`supply_count` 一类的字段必须区分两种语义并**显式命名**：`accessible_offering_count`（当前用户可见数，允许）与 `total_offering_count`（分组总数，**禁止**返回给非所有者）。
- **R-SCOPE-02b**：`group` 级 Grant 与 `offering` 级 Grant 在展示层的行为不同：前者可见该 group 下全部符合条件的供给，后者只可见目标供给。两者不得互相推导。

### 3.3 R-SCOPE-03：私有父分组不允许通过子供给邀请对外开放

**形式化**（与 §0.2 的 `effective_visibility` 一致）：

```text
eff(offering) = max( rank(group.visibility), rank(offering.visibility) )

can_issue_resource_invite(scope):
    scope.state == active
AND eff(scope) != private                # ← 硬门：private 恒不可签发
AND issuer is owner(scope)               # 或管理员按策略
```

- **R-SCOPE-03a**：父 `group.visibility == private` ⇒ 其**所有**子供给 `eff == private` ⇒ `can_issue_resource_invite` 对任何 scope（group 或 offering）**恒为 false**。签发请求返回确定性拒绝（`409 INVITE_NOT_PERMITTED` 或等价内部码；对外不泄露该分组是否存在，见 §7.2）。
- **R-SCOPE-03b（唯一合法路径）**：必须由分享者**显式变更父级策略**：

  ```text
  POST /api/share-groups/{group_id}/policy
  body: { "visibility": "restricted" | "public" }
  ```

  该操作必须：① 写审计事件；② 递增 `group.policy_version`；③ 使该 group 及其全部 offering 的授权与展示缓存失效（§6.2）；④ **不自动**为任何现有供给签发邀请，也不自动把已有 Grant 生效（Grant 生效是可见性的函数，见下）。
- **R-SCOPE-03c（反向收紧立即生效）**：`restricted → private` 或 `group.state → suspended/delisted` 的下调**立即**生效。已存在的 `active` Grant **记录不变**（不自动撤销、不缩短到期），但在收紧期间**不可见也不可调用**（因为 `private` / 状态门在 Grant 之上）。父级恢复为 `restricted` 后，这些 Grant **自动重新生效**，无需重新签发。
  - 理由：Grant 只解决 `restricted` 门槛（R-GRANT-02），这是确定性来源，避免了「Grant 与 visibility 谁优先」的歧义。
- **R-SCOPE-03d**：`private` 分组的所有者自己在**不使用** Grant 的情况下可见并可调用（所有权路径，R-GRANT-04）；这不构成「对外开放」。
- **R-SCOPE-03e**：渠道 Base URL、认证目标或模型归属的变更视为**新的资源修订**，必须重新验证、按审核策略重新发布，并使旧亲和缓存失效；**不得**利用改地址把原授权静默转给另一上游（计划书 §10.3 末段）。

---

## 4. 生命周期状态机（计划书 §10.3 逐格落地）

### 4.1 操作分类与边界定义

| 术语 | 定义 |
|---|---|
| **生成类操作** | 会创建新的上游调用或新对象的操作（新对话、新图片、新视频提交、衍生生成） |
| **维持类操作** | 针对**已存在**对象的操作（查询任务状态、取消任务、读取结果、列举自己的历史对象） |
| **未准入请求** | 尚未通过出站前重验（§6.3）的请求；拒绝它**不产生任何对象** |
| **已接收请求** | 已通过出站前重验并写入出站尝试记录（`admitted_at` 已落库）的请求 |
| **准入点 / 提交点** | 准入点 = 接收并解析请求；提交点 = `AuthorizeForOutbound` 通过并写入出站尝试。**「提交准入完成」是「请求是否已接收」的唯一分界**（计划书 §10.3） |

**R-LIFE-00（统一边界规则，适用于所有行）**

1. 任何生命周期事件都**只关闭生成类入口**（除 R4b 账户封禁外），**维持类入口**按对象所有权 + 当前身份状态 + 保留期继续开放。
2. 「已接收」不授予**后续上游尝试**的豁免权：每一次新的上游尝试（含重试、Key 切换、渠道切换）都必须重新通过 §6.3。因此「权限在请求中途被收紧」的确定结果是：**当前已发出的调用允许结束，后续尝试不再发生**。
3. 平台**从不**自动改用其他凭据或其他分享者重提；「改用其他凭据重提」只能由用户**显式发起新请求**，且新请求必须是其当前授权范围内的合法目标。
4. 已接收但结果未知的调用按计划书 §9.5/§9.2 处理：优先查询**原**任务，不在其他渠道重新提交；无法确认时保持 `unknown/reconciling`，不得假定上游未执行。

### 4.2 主矩阵（4 类事件 × 7 个行为格）

行为格的取值一律是确定动作；依据代码见 §4.3 图例（对同一行内所有格共同生效）。

| 事件（行） | C1 新请求（未准入） | C2 已接收在途 | C3 任务查询 | C4 任务取消 | C5 结果读取 | C6 上游凭据退役 | C7 换凭据/换来源重提 |
|---|---|---|---|---|---|---|---|
| **R1a 分享者普通暂停 / 渠道暂停 / 分组暂停** | **禁止**。该渠道/供给移出候选集；**可见性保留**（公开但不可用仍显示）。指定该来源 ⇒ **503** `NO_HEALTHY_TARGET` | **允许结束**；不主动中断已发送的上游调用；**不发起新的上游尝试** | **允许**（所有者 + 身份有效 + 保留期内） | **允许**发起；走原渠道与原凭据版本；上游不可达 ⇒ `cancelling → unknown`，**不重提** | **允许**（保留期内，鉴权访问；签名链接若启用则 ≤10 分钟且可撤销） | **否**（上游 Key 不退役，只是不再服务生成类请求） | **禁止自动**；用户可对**其他当前授权供给**显式发起**新**请求；固定来源 Key 不改绑 |
| **R1b 供给下架 / 分组下架** | **禁止**，且**可见性收回** ⇒ 直接指定返回 **404** `RESOURCE_NOT_ACCESSIBLE`（与「不存在」同形） | 同 R1a：**允许结束** | **允许**（历史对象所有权，§0.3 P3） | **允许**（同 R1a） | **允许**（保留期内） | **否** | **禁止自动**；用户显式新请求只能选其**当前可见**的供给 |
| **R1c 使用授权到期 / 撤销（Grant）** | **禁止**（`restricted` 门槛不再满足）⇒ 该供给对该用户**不可见** ⇒ **404** | **允许结束**；后续尝试因重验失败而不再发起 | **允许**；读取授权来自创建时的账号所有权，**不因授权到期被转交给他人** | **允许** | **允许**（保留期内） | **否** | **禁止**用「换供给重提」规避撤权；可对**当前仍获授权**的其他供给发起**新**请求 |
| **R2 上游 Key 普通轮换 / 退役** | **正常服务**：新调用使用新版本或其他合格 Key；池内无合格 Key ⇒ 渠道不接新请求 ⇒ **503** | **允许结束**，**不中途换 Key**（除非适配器确认凭据可共享，计划书 §9.2）；重试须重新选合格 Key | **允许**；**原凭据版本**为已有绑定任务保留必要的查询能力，至终态或配置的最长保留期限 | **允许**（同上，原凭据版本保留解引用能力） | **允许**（保留期内） | **是**（该 `credential_version → retired`，不再用于任何生成类请求；保留期后移除解引用能力） | **禁止**把已有任务改绑到池里下一把 Key；结果未知优先查询**原**任务，不在另一渠道重新提交；平台不自动换凭据重提 |
| **R3 上游 Key 泄露紧急吊销 / 管理员紧急封禁** | **立即停止**新的出站调用（按 `scope` 限定范围） | **尽力关闭流或取消任务**；无法确认者记 `unknown/reconciling`；**不发起任何新尝试** | 按 `scope`：含 `hide_results`（**默认含**）⇒ 仅返回最小状态元数据（`task_id`/`status`/`request_id`/时间），**不含结果引用与上游标识**；不含 ⇒ 允许完整查询；含 `deny_identity` ⇒ 401 | **允许**发起（止损动作）；因凭据已吊销，上游取消失败 ⇒ `cancelling → unknown` | 按 `scope`：含 `hide_results`（默认）⇒ **404**（不泄露存在性）；不含 ⇒ 允许（保留期内） | **是**（立即退役该 `credential_version`，**不可恢复**；包括任务查询/取消/续拉在内的任何出站一律禁用） | **明确禁止**：不改用同渠道其他 Key、不改用其他渠道、不换凭据重提（计划书 §10.3 明文） |
| **R4a 平台 Key 撤销（普通，非泄露）** | 该 Key **拒绝** ⇒ **401** `CREDENTIAL_INVALID` | **允许结束**（已发送调用不中断）；该请求的后续上游尝试因 Key 状态重验失败而**不再发起** | 该 Key **401**；**同账号其他 active Key 允许**（对象所有权属于账号，Key 只是凭据） | 同上 | 同上 | **是**（该 Key `revoked`，不可恢复、不可再认证） | **禁止自动换 Key**；允许用户用**同账号的 active Key 显式发起新请求**（这是新请求，不是平台重提） |
| **R4b 使用者账户封禁** | **拒绝** ⇒ **401** `ACCOUNT_DISABLED`（**所有**端点） | **尽力关闭流**；不再发起上游尝试 | **拒绝 401**（计划书 §10.3：「账户封禁则拒绝访问」） | **拒绝 401**（用户侧）；管理员侧保留取消/对账能力 | **拒绝 401** | **是**：该账号**全部**平台 Key 退役；**不退役**上游分享凭据（属分享者资产，与其他使用者无关） | **禁止**（账户级封禁，换 Key 无效） |

### 4.3 依据图例（逐格引用）

| 码 | 依据 |
|---|---|
| `A1` | 计划书 §10.3 生命周期表第 1 行（暂停/下架/授权到期撤销） |
| `A2` | 计划书 §10.3 生命周期表第 2 行（Key 普通轮换/退役） |
| `A3` | 计划书 §10.3 生命周期表第 3 行（Key 泄露紧急吊销、管理员紧急封禁） |
| `A4` | 计划书 §10.3 生命周期表第 4 行（账户封禁、平台 Key 撤销） |
| `B1` | 计划书 §10.3 表后第 1 段（历史任务读取授权来自创建时的用户所有权；当前身份状态与紧急封禁始终生效） |
| `B2` | 计划书 §10.3 第 1 段（凭据边界、日志/错误响应不得含凭据） |
| `C1` | 计划书 §9.1（指定来源不可用则返回原因，默认不悄悄改用其他分享者） |
| `C2` | 计划书 §9.2 末段（任务绑定创建它的凭据/账户版本，不参与普通轮询） |
| `C3` | 计划书 §9.5（结果未知优先查询原任务，不在另一渠道重新提交） |
| `D1` | 计划书 §10.2（暂停主要阻止新请求；已有任务按 §10.3 处理，避免孤儿任务） |
| `D2` | 计划书 §7 末段（软删除保留被引用的稳定 ID） |
| `E1` | 底座 `model/task.go:421-434`（`GetByTaskId(userId, taskId)` 按账号归属查询） |
| `E2` | 底座 `model/token.go:220-251`（`ValidateUserToken`：状态、到期、额度） |
| `E3` | 底座 `middleware/auth.go:56-58`（dashboard：用户非启用 ⇒ **401** `AUTH_USER_DISABLED`）与 `middleware/auth.go:447-451`（relay：账户非启用 ⇒ **403**）——两者**不一致**，按 §7.1 统一为 401 |
| `E4` | 底座 `model/channel.go:206-247`（多 Key 选择；无启用 Key 时返回错误，渠道不可用） |
| `E5` | 底座 `service/task_artifact_access.go:26-66,95-98`（产物能力 URL：仅绑定 `task_id + artifact_key`，**无用户绑定、无有效期**） |
| `E6` | 底座 `middleware/distributor.go:48-78`（pin 分支：渠道不存在 ⇒ 400，渠道停用 ⇒ 403） |
| `F1` | 计划书 §12（任务与必要凭据引用不提前清理；请求元数据 30 天、聚合 180 天；签名下载 ≤10 分钟且可撤销） |

### 4.4 逐行补充确定规则

- **R-LIFE-01（R1a/R1b 的判别）**：`suspended` 与 `delisted` 的差别**只体现在可见性**：`suspended` 保留可见（对外 503，标注不可用）；`delisted` 收回可见（对外 404）。二者对 C2–C7 的行为完全相同。这一划分是确定的，也解释了为什么「下架」不是「授权撤销」。
- **R-LIFE-02（R1c 的可见性推论）**：`restricted` 供给在 Grant 失效后对原用户不可见 ⇒ 对外 404。这不是「隐藏变更」，用户在**自己的**用量/任务页仍能看到历史归属与原因（不泄露他人信息，计划书 §3.4）。
- **R-LIFE-03（R2 的提示义务）**：Key 轮换、停用或移除**必须提示**对未完成任务的影响（计划书 §9.2）；历史任务的脱敏归属**不得**改绑到池里下一把 Key。
- **R-LIFE-04（R3 的 `scope` 必须是存储字段）**：紧急封禁记录必须显式保存 `scope`，默认值在创建时确定（`stop_outbound` 恒含；`hide_results` 默认含；`deny_identity` 账户级封禁时必含）。对外行为按 `scope` 求值——**这正是「已保存结果是否可读由管理员封禁范围决定」的确定性落地**（计划书 §10.3），而不是运行时模糊判断。
- **R-LIFE-05（R3 的凭据退役边界）**：紧急吊销后，该 `credential_version` **不可用于任何出站**，包含任务查询/取消/结果续拉。未完成任务进入 `unknown/reconciling`，由管理员对账或人工确认（计划书 §9.2）。
- **R-LIFE-06（R4a 的端点可见性）**：平台 Key 撤销只影响**凭据**，不影响**账号所有权**。因此「该 Key 401」与「同账号其他 Key 允许」同时成立，二者不矛盾，且都与计划书 §10.3 一致。
- **R-LIFE-07（R4b 与取消）**：账户封禁拒绝用户侧访问（含取消）；**管理员侧**取消/对账能力保留（计划书 §10.3 第 3 行明确管理员可「尽力关闭流或取消任务」）。
- **R-LIFE-08（保留期）**：所有「保留期内」均指计划书 §12 的开发基线（请求元数据 30 天、聚合 180 天；产物终态后默认 24 小时清理；尚未结束的任务及其必要凭据引用**不提前清理**）。保留期是**配置值**，不是「视情况」。
- **R-LIFE-09（签名链接）**：默认鉴权访问；若启用签名下载，使用**短期可撤销**链接，失效时间不超过结果保留期（计划书 §10.3、§12：默认最长 10 分钟）。底座现有能力 URL 是**长期有效且不绑定用户**（`service/task_artifact_access.go:95-98`，`middleware/task_artifact_access.go:172-217`），**必须改造**后才能满足本条。
- **R-LIFE-10（分享者视角）**：任何行、任何格，分享者**仍只见统计**；不得因暂停/撤权/封禁而获得使用者的邮箱、平台 Key、提示词或生成内容（计划书 §3.4、§10.3）。

### 4.5 与底座现状的差距（生命周期相关）

| 现状 | 位置 | 差距 |
|---|---|---|
| 任务查询按账号归属 | `model/task.go:421-434`、`controller/task.go:37-64`（`c.GetInt("id")`） | 可直接复用；但**不包含**「当前身份状态」这一层（如账户封禁后的拒绝需另加） |
| 任务产物内容走「凭据或能力 URL」二选一 | `router/task-router.go:28-36`（`TokenOrTaskArtifactAccessAuth("key","artifact_key")`） | 能力 URL 分支**完全绕过**账号与任务归属校验 |
| 能力 URL 仅绑定 `task_id + artifact_key`，且**无有效期** | `service/task_artifact_access.go:26-66`；注释明确「long-lived capability URL」`:95-98`；先验签后读库 `middleware/task_artifact_access.go:173,199` | 与 R-LIFE-09、§7.4 冲突，必须新增有效期与撤销机制，并明确传播范围（计划书 §5.2 第 4 条） |
| 账户非启用在不同入口返回不同状态码 | `middleware/auth.go:447-451`（403）vs `middleware/auth.go:56-58`（401） | 需统一（§7.1） |
| 逐 Key 状态只有启用/禁用 | `model/channel.go:225-233`（`getStatus` 只比较 `ChannelStatusEnabled`） | 缺 `cooling`/`expired`/`quota_exhausted`/`retired`/`revoked_emergency`；R2/R3 需要新增 |
| 每渠道轮询互斥锁**不是**并发上限 | `model/channel.go:219-221`、`:254-285`、`model/channel.go:642-651` | C6/C2 的容量语义需新建贯穿请求生命周期的容量控制（计划书 §9.2 [N4j]） |

> 未核实：`GetChannelPollingLock` 在多实例部署下的行为（单进程 `sync.Mutex`，跨实例无效）**未运行验证**；`model/channel.go:653-670` 的锁清理时机未逐行核对。

---

## 5. 权限变更的传播与出站前重验

### 5.1 版本向量

所有可变授权事实都带一个单调递增的版本字段（存**数据库**，为权威）：

| 版本 | 归属 | 递增触发 |
|---|---|---|
| `account.state_version` | 账号 | 封禁/解封/删除 |
| `key.state_version` | 平台 Key | 创建/撤销/退役/到期/额度耗尽 |
| `group.policy_version` | 分享分组 | 可见性、状态、owner 相关策略变更（R-SCOPE-03b） |
| `offering.policy_version` | 供给 | 可见性、状态、能力、模型归属变更 |
| `grant.state_version` | Grant | 签发/撤销/到期落库 |
| `channel.config_version` | 渠道 | 暂停、并发/速率/预算配置、Base URL、认证目标 |
| `upstream_key.credential_version` | 上游凭据 | 轮换、退役、紧急吊销 |
| `price.version` | 价格 | 价格快照发布（影响 USD 预算类判定） |

**R-VER-01（聚合戳）**：单次判定把用到的版本聚合成一个 `AuthzStamp`：

```text
AuthzStamp = H(account.state_version, key.state_version, group.policy_version,
               offering.policy_version, grant.state_version, channel.config_version,
               upstream_key.credential_version, price.version)
```

`AuthzStamp` 与路由决定一起**持久化到 Request / Attempt / AsyncTask**，用于事后解释与对账（计划书 §9.2「配置版本」、§7「权限版本快照」）。

**R-VER-02（账号级连带）**：以 `key_effective_version = max(key.state_version, account.state_version)` 求值，避免账号封禁时必须逐把 Key 递增（但仍要求**同事务**递增 `account.state_version`）。

### 5.2 缓存与失效

- **R-CACHE-01**：缓存条目的键**必须包含** `AuthzStamp`；只有 stamp 完全一致时条目才可用。TTL 仅用于控制内存与陈旧度上限（建议 ≤ 30 秒，可配置），**不得**作为正确性依赖。
- **R-CACHE-02（deny 不缓存）**：**拒绝**结论一律实时求值、不写入缓存。允许结论可缓存但必须 stamp 匹配。这条规则消除了「已撤销但仍可用」的窗口。
- **R-CACHE-03（写路径）**：状态变更与版本递增在**同一数据库事务**内完成；Redis 侧只做失效广播，不做权威。可参照底座的 fence 思路——`model/token.go:311-314` 注释明确「写库前失效缓存并设置 fence，防止并发读者把过期快照重新写回缓存」。
- **R-CACHE-04**：版本变更必须使**所有**派生缓存失效，包括：可见性列表、来源计数、`/v1/models` 结果、亲和缓存（§5.5）、容量预读。
- **R-CACHE-05（不假设跨库事务）**：PostgreSQL 事务管持久状态，Redis 管并发/速率预留；执行顺序为「记录意图与预留 → 取得容量 → **复验权限** → 记录出站尝试 → 发送」（计划书 §9.2；CODE_STYLE 第 8 条）。

### 5.3 R-REVAL-01：「提交准入完成」边界

**边界定义（唯一）**

```text
admitted_at = 第一次成功通过 AuthorizeForOutbound 并写入出站尝试记录的时刻
请求状态：
  now <  admitted_at  →  「未准入」：拒绝不产生任何对象，可安全返回错误
  now >= admitted_at  →  「已接收」：按 §4 生命周期矩阵处置（允许结束，不中断）
```

**R-REVAL-02（函数契约，Web 与 Worker 共用同一实现）**

```text
function AuthorizeForOutbound(subject, target, op, attempt_index, request_id) -> OK | DENY(reason):

    # 输入必须显式传参，禁止依赖 HTTP 中间件挂在请求上下文里的隐式状态
    subject = { account_id, key_id }
    target  = { offering_id, channel_id, upstream_key_id, credential_version }
    op      = generate | query | cancel | read_result

    # 1) 实时重读最小列（不读缓存、或读缓存但必须 stamp 匹配）
    if load_account(subject.account_id).state != active:                 return DENY(ACCOUNT_DISABLED)
    if subject.key_id != null and op == generate:
        k = load_key(subject.key_id)
        if k.state != active:                                            return DENY(CREDENTIAL_INVALID)
    if load_group_of(target.offering_id).state != active:                return DENY(POLICY_DENIED)
    if load_offering(target.offering_id).state != active:                return DENY(POLICY_DENIED)
    if not visibility_and_grant_ok(subject, target):                     return DENY(POLICY_DENIED)

    # 2) 凭据版本与容量预留
    cred = load_upstream_key(target.upstream_key_id)
    if cred.credential_version != target.credential_version:             return DENY(CREDENTIAL_RETIRED)
    if cred.state in { retired, revoked_emergency, expired, disabled, cooling, quota_exhausted }:
                                                                        return DENY(CREDENTIAL_RETIRED)
    if not reservation_still_valid(target, request_id, attempt_index):   return DENY(CAPACITY)

    # 3) 记录出站尝试（含 AuthzStamp），此后请求即为「已接收」
    record_outbound_attempt(request_id, attempt_index, target, authz_stamp())
    return OK
```

- **R-REVAL-03（粒度 = 每次尝试）**：`AuthorizeForOutbound` 在**每一次**出站尝试前调用，包括首投、重试、Key 切换、渠道切换、任务续拉、任务查询、任务取消。因此「已接收」不产生豁免权（R-LIFE-00 第 2 条）。
- **R-REVAL-04（失败处置）**：
  - HTTP 阶段失败 ⇒ 不发送，幂等释放本次预留，按 §7.1 返回对应错误码。
  - Worker 阶段失败 ⇒ **不得**无限重排等待；任务置终态失败 `denied_by_policy`，保留可查询的原因元数据（不含凭据、不含上游地址）。
- **R-REVAL-05（幂等补偿）**：预留的归还必须幂等（唯一 `attempt_id`）；进程在步骤之间退出由后台对账处理（计划书 §9.2）。
- **R-REVAL-06（禁止隐式来源）**：底座把 token 限制、额度、分组写入 `gin.Context`（`middleware/auth.go:506-524`）。这些值**只能**作为 HTTP 层的快速预筛；**权威判定必须重新从数据库/带 stamp 的缓存读取**，否则 Worker（无 `gin.Context`）无法复用同一规则。

### 5.4 R-REVAL-07：Web 与 Worker 使用同一规则

| 入口 | 调用形式 | 要求 |
|---|---|---|
| Web（同步/流式 relay） | `middleware.Distribute()`（`middleware/distributor.go:34`）之后、发送上游前调用 `AuthorizeForOutbound` | S1–S11 与 §6.3 全部执行；指定来源与自动路由走同一函数 |
| Web/Worker（异步任务提交） | 任务提交路径（原渠道 pin 见 `relay/relay_task.go:174`、`dto/channel_constraints.go:71-117`） | 与 Web 同一函数；原渠道 pin **不豁免**授权检查 |
| Worker（任务轮询/续拉/查询/取消） | 无 HTTP 上下文，显式传 `subject/target/op` | 同一函数；`op ∈ {query, cancel}` 时按 §4 的行规则决定是否放行 |
| 亲和缓存命中 | `middleware/distributor.go:126` | 命中后仍执行同一函数 |

- **R-REVAL-08**：所有客户端协议入口（OpenAI 兼容、Anthropic、Gemini 原生等）必须走同一函数；**不允许**任何「管理员后缀」类旁路（计划书 §9.1）。
- **R-REVAL-09**：平台扩展请求头（`X-APIGX-Offering`、`X-APIGX-Route` 及平台 Key）在**出站前剥除**，上游不得收到平台 Key 或任何内部授权信息（计划书 §9.1；CODE_STYLE 第 10 条）。

### 5.5 R-REVAL-10：资源修订与亲和缓存失效

渠道 `Base URL`、认证目标或模型归属变更 ⇒ 视为**新的资源修订**：

1. `channel.config_version`（或供给 `revision`）递增；
2. 重新验证并按审核策略重新发布（不可静默生效）；
3. 使旧亲和缓存**全部失效**；
4. 旧授权**不自动迁移**到新目标（不允许通过改地址把原授权转给另一上游）。

---

## 6. 对外错误码与不泄露规则

### 6.1 错误码表（对外唯一权威）

| HTTP | `code` | 触发条件 | 备注 |
|---|---|---|---|
| **404** | `RESOURCE_NOT_ACCESSIBLE` | 供给**不存在**，或对当前请求者**不可见**（私有、他人私密、已下架、无有效 Grant、模型不在允许集合之外的来源层判定） | 两种情况响应体**完全一致**，避免枚举私有来源 |
| **401** | `CREDENTIAL_INVALID` | 平台 Key 不存在 / 已撤销 / 已退役 / 已过期 / 额度耗尽 / 格式非法 | 计划书 §9.1 |
| **401** | `ACCOUNT_DISABLED` | 账号被封禁（**所有**端点，含查询与读结果） | 统一 dashboard（`middleware/auth.go:56-58`）与 relay（`middleware/auth.go:447-451` 现为 403）的不一致 |
| **400** | `PARAM_CONFLICT` | 同时提交 `X-APIGX-Offering` 与 `X-APIGX-Route`；固定来源 Key 携带 `X-APIGX-Route`；`X-APIGX-Offering` 与固定来源不一致；来源 id 不在 `allowed_offerings` | 计划书 §9.1 |
| **400** | `PARAM_INVALID` | 请求参数本身非法（含超出规格上限的上界校验、`cascade` 缺失、模型名歧义无法唯一解析） | 计费安全不变量要求倍数类输入必须有上界（底座 AGENTS.md） |
| **429** | `CAPACITY_EXCEEDED` | 授权范围内**有**候选但暂时满载/限流（含**指定来源**满载，不因指定而突破） | 可计算时附 `Retry-After` |
| **503** | `NO_HEALTHY_TARGET` | 授权范围内**无**可调用渠道（供给/分组暂停且仍可见、池内无合格 Key、凭据被吊销、无健康渠道） | 计划书 §9.1 |

**R-ERR-01（404/503 的判别式，确定）**

```text
若 目标供给对请求者「不可见」            → 404 RESOURCE_NOT_ACCESSIBLE
若 目标供给「可见」但当前不可调用          → 503 NO_HEALTHY_TARGET
若 授权范围内有可调用候选但暂时满载        → 429 CAPACITY_EXCEEDED
```

即 **可见性决定 404，可用性决定 503，瞬时容量决定 429**。三者不重叠。

**R-ERR-02（不使用 403 表达对外资源授权失败）**：对外调用入口不使用 403（避免与 401/404 混淆、避免泄露「存在但你没权限」）。403 仅用于**内部管理接口**的角色不足（底座已有 `AUTH_INSUFFICIENT_PRIVILEGE`，`middleware/auth.go:61`）。

**R-ERR-03（顺序）**：认证（401）**先于**可见性（404）；未认证请求不透露任何对象的存在性。

### 6.2 请求 ID（脱敏）

- **R-ERR-10**：请求 ID 由**服务端**生成（底座现状：`middleware/request-id.go:10-18` 生成并回写响应头；`common/utils.go:281-283` 生成规则）。**不接受**客户端提供的请求 ID 作为权威标识。
- **R-ERR-11（脱敏含义）**：对外返回的请求 ID **不得**编码任何可识别信息——不含 `account_id`、`key_id`、`channel_id`、`offering_id`、上游 ID、任务 ID；仅为 `时间戳前缀 + 随机`（底座的 `GetTimeString() + prefix + 随机` 形态符合此要求，`common/utils.go:273-283`）。
- **R-ERR-12**：请求 ID 出现在响应体（`common.MessageWithRequestId`，`common/utils.go:293-295`）与响应头（`common.RequestIdKey = "X-Oneapi-Request-Id"`，`common/constants.go:185-186`）。内部日志保留**精确**归因，外部错误**只**给请求 ID。

### 6.3 R-ERR-20：错误响应禁止包含的内容

| 禁止项 | 说明 |
|---|---|
| 上游 Key、上游凭据任何部分（含脱敏前后缀） | 计划书 §10.3、CODE_STYLE 第 4 条 |
| 平台 Key（明文、摘要、部分） | 同上 |
| 上游 Base URL、域名、IP、认证目标等地址细节 | 计划书 §9.1 |
| 其他使用者的邮箱、用户名、平台 Key、用量、提示词、生成内容 | 计划书 §3.4、§10.3 |
| 上游 Key 池成员的名称与内部 ID | 计划书 §3.5 |
| SQL/堆栈/内部配置键等实现细节 | ASVS 16.5.1 |
| 任务产物的长期能力 URL | 见 R-LIFE-09，能力 URL 必须短期可撤销 |

- **R-ERR-21**：响应体形状与底座一致（`{"error":{"message","type","code"}}`，`middleware/utils.go:14-36`），`type = "new_api_error"`；新增 `code` 取值必须来自 §6.1 表。
- **R-ERR-22**：分享者视角的统计同样脱敏（计划书 §3.4）。
- **R-ERR-23**：审计与监控**标签**同样不得携带凭据（计划书 §10.3；ASVS 16.2.5）。

### 6.4 审计事件（ASVS 强制）

- **R-ERR-30（必须记录）**：认证操作成功与失败（含失败原因分类，不含凭据）；授权失败（含被拒的供给/模型标识、请求者标识）；邀请的创建/兑换/撤销；Grant 的签发/撤销/到期；暂停、下架、轮换、紧急吊销、账号封禁、平台 Key 撤销；权限版本变更。（ASVS 16.3.1、16.3.2、16.2.1）
- **R-ERR-31（不得记录）**：密码、验证码、恢复码、私钥、**可用的**会话或认证令牌、邀请码明文、平台 Key、上游 Key。（底座 AGENTS.md 认证安全条款；ASVS **16.2.5**）
- **R-ERR-32**：时间戳统一 UTC（ASVS 16.2.2；CODE_STYLE 第 3 条）。

---

## 7. OWASP ASVS 覆盖（注册 / 登录 / 邀请相关流程）

底座强制规范要求「涉及认证流程的改动必须符合 OWASP ASVS 与 Cheat Sheet，服务端强制，前端校验不能替代；审计事件不得含凭据」（`docs/stage0/base-lock.md` §6；`new-api/AGENTS.md`）。下表给出本文落点。

**版本与核实状态**：以下条目号取自 OWASP ASVS **5.0** 的 V6（Authentication）与 V16（Security Logging and Error Handling）章节正文，已按原文核对条目号与描述。**V7（Session Management）与 V2（Validation and Business Logic）的具体条目号未逐条核对**，只按章节级引用。

| ASVS 5.0 | 要求要点 | 本文落点 | 状态 |
|---|---|---|---|
| 6.1.1 | 限流、反自动化、自适应响应须有文档化配置 | R-INV-25（兑换限流）；§6.1 的 429 语义 | 设计已定，实现未做 |
| 6.2.1 / 6.2.5 / 6.2.9 | 口令长度与字符集规则 | 沿用底座既有注册流程（`controller/user.go:236` 的 `common.Validate.Struct`） | 未核实底座校验标签的具体长度下限（**未读 `model/user.go` 的 `Password` 标签**） |
| 6.2.4 / 6.2.12 | 弱口令 / 已泄露口令检查 | 不在本文范围（属注册流程实现），登记为待办 | 未覆盖 |
| 6.3.1 | 防撞库与暴力破解（按文档配置） | R-INV-25；R-ERR-30（失败留痕） | 设计已定 |
| 6.3.2 | 无默认账号 | 不在本文范围 | 未覆盖 |
| 6.3.4 | 多认证路径不得有未记录旁路，且强度一致 | **§5.4**（所有协议入口与 Worker 走同一授权函数）、§1.5（pin 旁路必须收敛） | 设计已定 |
| 6.3.7 | 认证细节变更后通知用户 | 不在本文范围（属通知实现） | 未覆盖 |
| 6.3.8 | 有效用户不可被推断（含注册与找回流程） | **§6.1 R-ERR-01/03**、**R-INV-10 统一错误码**、R-INV-25 | 设计已定 |
| 6.4.1 | 系统生成的初始口令/激活码：CSPRNG、符合口令策略、短有效期或首次使用即失效、不得成为长期口令 | **R-INV-21**（CSPRNG + 高熵）、**R-INV-22**（确定有效期）、**R-INV-23**（一次性） | 设计已定 |
| 6.5.1 | 一次性凭据仅可使用一次 | **R-INV-23**（一次性不可重放）、R-INV-10b（幂等） | 设计已定 |
| 6.5.2 | 低熵凭据的存储须用带 32 位随机盐的密码存储算法 | **R-INV-20**（仅存域分离 HMAC 摘要；邀请码熵 ≥112 bit 时标准哈希函数可接受） | 设计已定；具体算法与盐策略待实现确认 |
| 6.5.3 | CSPRNG 生成 | **R-INV-21** | 设计已定 |
| 6.5.4 | 凭据熵下限 | **R-INV-21**（≥112 bit，高于 20 bit 下限） | 设计已定 |
| 6.5.5 | 凭据必须有确定有效期 | **R-INV-11 / R-INV-22** | 设计已定 |
| 6.5.6 | 任何认证因子可被撤销 | **R-INV-12 / R-INV-24**（邀请可撤销）、§4 R2/R3/R4a（凭据可撤销/退役） | 设计已定 |
| 6.6.2 / 6.6.3 | 带外码绑定原请求且不可复用；码校验限流 | R-INV-25、R-INV-10c（类型域分离，不可跨用途复用） | 设计已定 |
| 16.2.1 / 16.2.2 | 日志元数据（who/when/what）与 UTC 时间 | **R-ERR-30 / R-ERR-32** | 设计已定 |
| 16.2.5 | 按数据保护级别决定是否记日志：凭据**不得**入日志；会话令牌只能哈希/掩码 | **R-ERR-31**（明确禁止清单） | 设计已定 |
| 16.3.1 / 16.3.2 | 记录全部认证操作（成功/失败）与授权失败 | **R-ERR-30** | 设计已定 |
| 16.3.3 | 记录绕开安全控制的尝试 | R-ERR-30（参数冲突/越权尝试留痕）、R-INV-25 | 设计已定 |
| 16.4.1 | 日志注入编码 | 沿用底座日志组件；本文只规定字段 | 未核实底座日志编码实现 |
| 16.5.1 | 面向消费者的通用错误消息，不暴露内部数据 | **§6.3 R-ERR-20/21** | 设计已定 |
| 16.5.3 | 失败须安全失败，不得 fail-open | **R-REVAL-04**（不发送即释放预留）、§4 R1b/R1c（拒绝即拒绝，不降级放行） | 设计已定 |
| V7（章节级） | 会话管理：会话轮换、失效、CSRF、注销 | 本文只覆盖「平台 Key 撤销/退役/封禁后的会话与凭据处置」（§4 R4a/R4b）；其余属注册/登录实现范围 | **条目号未核实**；覆盖不完整 |
| V2（章节级） | 输入校验与业务逻辑（含业务逻辑滥用） | §1.4 R-PARAM-01（参数不能扩大授权）、R-ERR-02 | **条目号未核实** |

**明确不做声明的事**：本文**不声称** ASVS 合规；上表的「设计已定」只表示状态流已给出确定规则，**未实现、未测试、未验证**。

---

## 8. 关键场景期望结果

| # | 场景 | 期望结果（确定） | 依据规则 |
|---|---|---|---|
| ① | **私有资源不被枚举** | 未获授权用户：广场/搜索/来源计数/`/v1/models`/模型详情的来源列表**均不含**该供给；直接指定其 `offering_id` ⇒ **404 `RESOURCE_NOT_ACCESSIBLE`**，且响应体、状态码、耗时与「该 id 不存在」不可区分；不得通过条数差、排序位置、错误码或 429/503 的存在性推断其存在 | §3.1、§3.2、R-ERR-01、R-ERR-03 |
| ② | **固定来源 Key 不能被请求头放宽** | `pinned_offering = O1` 的 Key 携带 `X-APIGX-Offering: O2` ⇒ **400 `PARAM_CONFLICT`**（即使 `O2` 存在且对用户可见）；携带任意 `X-APIGX-Route` ⇒ **400 `PARAM_CONFLICT`**；省略头 ⇒ 仍只用 `O1`。**任何情况下不会调用到 `O2`** | R-HARD-03、R-PARAM-01c、S5/S6 |
| ③ | **指定来源失效时不自动换分享者** | 用户指定 `O1`，`O1` 被分享者暂停（仍可见）⇒ **503 `NO_HEALTHY_TARGET`**。**不发生**自动回退到其他分享者的供给。仅当 Key 的 `allow_fallback = true` 且回退目标 ⊆ `allowed_offerings` 且回退目标自身通过 S8–S11 时，才允许回退（并在响应/日志中标注回退事实） | C1、R-HARD-04、R-HARD-04a、S11/S12 |
| ④ | **撤权后仍能读取自己历史对象** | Grant 到期/撤销、或分享者暂停/下架之后：该用户在**保留期内**仍可查询、取消、读取**自己**此前提交的任务与结果（鉴权访问）；该读取授权**不转交**他人；分享者仍只见统计。若期间发生**紧急封禁且 scope 含 `hide_results`**，则读结果改为 404（这是唯一例外） | B1、R-LIFE-02、E1、R-LIFE-04、R-LIFE-10 |
| ⑤ | **Key 紧急吊销后不换凭据重提** | 上游 Key 被紧急吊销 ⇒ 该 `credential_version` **禁止任何出站**（含查询/取消/续拉）；在途任务尽**力**关闭/取消，无法确认者记 `unknown/reconciling`；**明确禁止**改用同渠道其他 Key、其他渠道或其他分享者重提；未确认的任务由管理员对账 | A3、R-LIFE-04、R-LIFE-05、C3 |
| ⑥ | **账户封禁后拒绝访问** | 封禁账号以任意平台 Key 发起任何请求（生成、查询、取消、读结果）⇒ **401 `ACCOUNT_DISABLED`**；该账号全部平台 Key 退役；**不**退役分享者的上游凭据；换成同账号另一把 Key 仍然 401 | A4、E3、R-LIFE-06、R-LIFE-07、§6.1 |
| ⑦ | **邀请码并发兑换只成功一次（额度上限生效）** | `max_uses = 1` 的邀请码被两个并发请求兑换 ⇒ **恰好一个**成功并产出 1 条 Grant；另一个得到统一错误 `ERR_INVITE_UNAVAILABLE`，且 `redeemed_count` 不超过 `max_uses`。重复兑换同一码的**同一账号**返回同一 `grant_id` 且不重复扣减 | R-INV-10、R-INV-10a、R-INV-10b |
| ⑧ | **私有父分组不能通过子供给邀请对外开放** | 父分组 `visibility = private` 时，对其任一子供给签发资源邀请 ⇒ **确定性拒绝**；唯一合法路径是分享者先显式把父级改为 `restricted`/`public`（写审计 + 递增 `group.policy_version` + 全量缓存失效），之后才能签发；反向收紧（→ `private`）立即生效，已存在的 Grant 记录不变但暂不可用，父级恢复后自动重新生效 | R-SCOPE-03a、R-SCOPE-03b、R-SCOPE-03c、R-GRANT-02 |

补充两个边界场景（用于交叉验证同一套规则）：

| # | 场景 | 期望结果 | 依据 |
|---|---|---|---|
| ⑨ | 撤销邀请且 `cascade = none` | 邀请不可再兑换；由它产生的 Grant **保持 active** 到自身到期 | R-INV-13 |
| ⑩ | 撤销邀请且 `cascade = generated` | 邀请不可再兑换 **且** 由它产生的 active Grant 同事务置 `revoked`；**手工**指定的 Grant（`origin_invite_id IS NULL`）不受影响 | R-INV-13 |

---

## 9. 落地依据索引（底座代码）

所有路径相对 `new-api/`，固定提交 `69a50029819a26c53e6babd276d49cfe2f8880ad`。

### 9.1 鉴权与授权

| 位置 | 现有行为 | 本文用法 |
|---|---|---|
| `middleware/auth.go:47-81` | `authHelper`：dashboard 鉴权主链、用户状态、角色下限、管理操作审计兜底 | S2 的 401 语义来源；审计挂载点参考 |
| `middleware/auth.go:56-58` | 用户非启用 ⇒ **401** `AUTH_USER_DISABLED` | §6.1 `ACCOUNT_DISABLED` 的统一目标 |
| `middleware/auth.go:60-63` | 角色不足 ⇒ 403 `AUTH_INSUFFICIENT_PRIVILEGE` | R-ERR-02（403 仅限内部管理接口） |
| `middleware/auth.go:100-116` | `UserAuth` / `AdminAuth` / `RootAuth` | 分享者接口不得复用 `AdminAuth` |
| `middleware/auth.go:361-480` | `TokenAuth`：Key 校验 → IP 限制 → 账户状态 → 分组 → `SetupContextForToken` | S1/S2 的复用基础 |
| `middleware/auth.go:405-421` | Key 无效 ⇒ 401 | §6.1 `CREDENTIAL_INVALID` |
| `middleware/auth.go:424-438` | 令牌 IP 白名单 ⇒ 403 | 属 Key 硬限制维度（R-HARD-06 同类） |
| `middleware/auth.go:447-451` | 账户非启用 ⇒ **403**（与 `:57` 的 401 不一致） | **必须统一为 401** |
| `middleware/auth.go:455-471` | token 分组必须 ∈ 用户可用分组；否则 403 | 底座「用户分组」≠ 分享分组（§2 边界 1） |
| `middleware/auth.go:506-535` | `SetupContextForToken`：模型限制、额度、分组、跨组重试写入 context | R-REVAL-06：仅作预筛，权威判定需重新求值 |
| `middleware/auth.go:536-553` | 指定渠道（`parts`）仅管理员；普通用户 403 | §1.5：改为基于 `allowed_offerings` 的授权 |
| `middleware/distributor.go:34-47` | `Distribute()` 入口，取 model/constraints | Web 侧接入 `AuthorizeForOutbound` 的位置 |
| `middleware/distributor.go:48-78` | **pin 分支**：渠道存在（400）/启用（403）/过滤器（400） | **不含 token 模型限制 → 旁路（§1.5）** |
| `middleware/distributor.go:79-99` | 非 pin 分支才做 `token_model_limit` 校验（403） | 与 pin 分支合并为同一函数 |
| `middleware/distributor.go:101-193` | 选路；无可用渠道 ⇒ **503**（`:178`、`:182`、`:193`） | §6.1 `NO_HEALTHY_TARGET` |
| `middleware/distributor.go:126` | 亲和缓存命中 `GetPreferredChannelByAffinity` | 命中后仍须完整授权（R-REVAL-08、§5.5） |
| `middleware/distributor.go:613` | `SetupContextForSelectedChannel` | 选定渠道后写 context；容量/凭据校验应在其后、发送之前 |
| `dto/channel_constraints.go:5-8,71-117` | pin 来源（token=rank 0 / origin_task=rank 10）、重试抑制 | 指定来源与任务原渠道的底座机制；**不构成授权** |
| `middleware/model-rate-limit.go:80-133` | 用户级窗口限流 ⇒ 429 | §6.1 `CAPACITY_EXCEEDED` 的复用点之一 |
| `service/group.go:14-43`、`setting/ratio_setting/group_ratio.go:66` | 用户可用分组 / 分组倍率存在性 | 分享分组必须独立成表，不复用 |

### 9.2 渠道与多 Key

| 位置 | 现有行为 | 本文用法 |
|---|---|---|
| `router/channel-router.go:19-37` | `/api/channel` 整组 `AdminAuth()`；`/:id/key` 另有 `RootAuth` + 限流 + 禁用缓存 + 安全验证 | 分享者渠道接口必须另建，不复用该组 |
| `router/channel-router.go:39-82` | 40+ 条管理路由按 `authz.Permission` 分权 | 同上 |
| `model/channel.go:23-60` | `Channel` 字段清单**无 owner** | 需新增 owner 归属 |
| `model/channel.go:206-247` | `GetNextEnabledKey`：多 Key 池，无启用 Key ⇒ 错误（渠道不可用） | R2 的「池内无合格 Key ⇒ 503」 |
| `model/channel.go:219-221,254-285` | 每渠道轮询互斥锁 + 轮询索引 | **不是**并发上限（计划书 §9.2 [N4j]） |
| `model/channel.go:225-233` | 逐 Key 状态仅「启用/禁用」（默认启用） | 缺 cooling/expired/quota_exhausted/retired/revoked_emergency |
| `model/channel.go:642-651` | `GetChannelPollingLock`（进程内 `sync.Map` + `sync.Mutex`） | 多实例下不构成全局互斥（**未核实**其跨实例行为） |
| `model/channel.go:673-724` | `handlerMultiKeyUpdate`（逐 Key 状态更新） | R2 轮换/退役的落点参考 |
| `model/channel_cache.go:219-286` | `CacheGetChannel` / `CacheUpdateChannelStatus` / `CacheUpdateChannel` | §5.2 缓存失效的复用点 |
| `common/constants.go:255-258` | `ChannelStatusUnknown/Enabled/ManuallyDisabled/AutoDisabled` | §0.2 状态映射 |

### 9.3 账号、凭据与邀请

| 位置 | 现有行为 | 本文用法 |
|---|---|---|
| `controller/user.go:215-223` | 注册开关（`RegisterEnabled`、`PasswordRegisterEnabled`） | 注册准入的服务端强制形态 |
| `controller/user.go:236-238` | `common.Validate.Struct(&user)` 输入校验 | 密码策略落点（**未核实**具体长度下限） |
| `controller/user.go:240-256` | 邮箱验证码校验 + 邮箱可用性 | 与注册邀请 **AND** 组合（R-INV-04） |
| `controller/user.go:262-270` | 重名/重邮箱检查 | 反枚举相关（6.3.8） |
| `controller/user.go:272-273` | `affCode` → `GetUserIdByAffCode`，**错误被丢弃** | `aff_code` ≠ 注册邀请（§2.6） |
| `common/constants.go:64-71` | `RegisterEnabled` / `PasswordRegisterEnabled` / `EmailVerificationEnabled` | 仅有开关，无邀请机制 |
| `common/verification.go:16-56` | 验证码 purpose 前缀 + 10 分钟有效 + **进程内 map** | purpose 分离思路可参考；**不可**照搬内存 map |
| `model/token.go:14-33` | `Token` 字段：`Key`（明文列）、`Status`、`ExpiredTime`、`RemainQuota`、`ModelLimitsEnabled/ModelLimits`、`Group`、`CrossGroupRetry`、`AutoGroups` | 平台 Key 的既有维度；缺 `pinned_offering`/`allowed_offerings`/`allow_fallback`/`allowed_sharers` |
| `model/token.go:220-258` | `ValidateUserToken`：状态、到期、额度 | S1 的复用基础 |
| `model/token.go:280-301` | `GetTokenByKey`：按**明文** key 查询 | 与「只存摘要」冲突（需改造） |
| `model/token.go:303-317` | `Insert`/`Update`；写库前失效缓存 + fence | R-CACHE-03 的参照 |
| `model/token.go:334-352` | `GetModelLimitsMap`（模型限制） | R-HARD-01 的复用基础 |
| `model/user.go:86-87` | `Role` / `Status`（1 enabled / 2 disabled） | §0.2 映射 |
| `model/user.go:1223-1233` | `IsAdmin` | 不得作为分享者接口的授权依据 |

### 9.4 任务与产物

| 位置 | 现有行为 | 本文用法 |
|---|---|---|
| `router/task-router.go:14-26` | 任务提交/查询/产物列表走 `TokenAuth()` | 维持类操作的鉴权基础 |
| `router/task-router.go:28-36` | 产物内容走 `TokenOrTaskArtifactAccessAuth("key","artifact_key")` | **能力 URL 分支绕过账号与归属校验** |
| `middleware/task_artifact_access.go:172-217` | 先验签（无数据库读取）再放行；失败统一 404；有匿名限流 | 可作为反枚举形态参考；缺用户绑定与有效期 |
| `service/task_artifact_access.go:26-66` | `IssueTaskArtifactAccess` / `VerifyTaskArtifactAccess`：HMAC over `(task_id, artifact_key)`，常数时间比较 | **无用户绑定、无过期字段** |
| `service/task_artifact_access.go:95-98` | 注释明确「long-lived capability URL」 | 与 R-LIFE-09 冲突，必须改造 |
| `controller/task.go:37-64` | `GetTask`：`model.GetByTaskId(c.GetInt("id"), ...)` | 账号归属校验（E1） |
| `controller/task.go:66-90` | `GetTaskArtifacts`；构建产物内容 URL（`:101`） | 需加保留期与撤销语义 |
| `controller/task.go:279` | `TaskArtifactContent` | 能力访问的落点 |
| `model/task.go:421-434` | `GetByTaskId(userId, taskId)`：`WHERE user_id = ? AND task_id = ?` | 所有权路径的直接依据 |

### 9.5 错误体、请求 ID 与日志

| 位置 | 现有行为 | 本文用法 |
|---|---|---|
| `middleware/utils.go:14-36` | `abortWithOpenAiMessage`：统一错误体 `{error:{message,type,code}}` + 请求 ID + 内部日志 | R-ERR-21 的复用点 |
| `middleware/request-id.go:10-18` | 服务端生成请求 ID，注入 context 与响应头 | R-ERR-10/12 |
| `common/constants.go:185-186` | `RequestIdKey = "X-Oneapi-Request-Id"`、`UpstreamRequestIdKey` | R-ERR-11/12；上游请求 ID 属内部信息，不得出现在对外错误里 |
| `common/utils.go:273-295` | `NewRequestId`（时间戳 + 前缀 + 随机）、`MessageWithRequestId` | R-ERR-11（脱敏） |
| `controller/relay.go:72,94,219,232` | 错误消息附请求 ID | R-ERR-12 |
| `middleware/auth.go:70-80`、`model/audit_log.go:43,55,116-135` | 管理操作审计兜底、审计日志结构与失败处理 | R-ERR-30/31 的落点参考 |

---

## 10. 未核实与已知限制

以下条目**没有被证实**，禁止在后续工作中当作已确认结论使用。

**A. 底座代码层面**

1. `service/authz` 的 `Permission` 枚举与 Casbin 策略定义**未读取**；`RequirePermission`、`SecureVerificationRequired`、`CriticalRateLimit`、`DisableCache`（`router/channel-router.go:24-34`）的内部实现**未核实**。
2. **未核实**：`GetChannelPollingLock`（`model/channel.go:642-651`）在多实例部署下的实际行为——它是进程内 `sync.Mutex`，跨实例是否为全局互斥，本文未运行验证。
3. **未核实**：任务产物能力 URL 是否存在本文未发现的过期或撤销控制。已检索 `VerifyTaskArtifactAccess` / `IssueTaskArtifactAccess` 的全部调用点（`controller/task.go`、`middleware/task_artifact_access.go`、两个测试文件），未见过期逻辑；但**反向代理层或部署配置是否另有限制未核实**。
4. **未核实**：底座是否已存在独立的 Worker 进程模式。本文只规定「Web 与 Worker 必须调用同一授权函数」，不描述其进程划分。
5. **未核实**：`model/user.go` 的 `Password` 校验标签具体长度下限（ASVS 6.2.1/6.2.9 的落点）。
6. **未核实**：日志组件的日志注入编码实现（ASVS 16.4.1）。
7. **未核实**：底座注册流程是否已有反枚举的统一响应（`controller/user.go:262-270` 当前分别返回 `MsgUserExists`、`MsgUserEmailAlreadyTaken` 等区分性错误——**这是否构成用户枚举通道尚未评估**，只记录观察到的现象）。

**B. 检索级（非穷尽证明）结论**

8. 「底座不存在注册邀请码机制」的结论来自对 `*.go` 检索 `InviteCode|invite_code|InviteEnabled` 未命中。这是**检索级**结论，未穷尽前端代码、迁移脚本与 `plugins/` 目录。
9. 计划书 §13 列出的 [N4b]/[N4d]/[N4f] 三条源码线索已在本轮逐条复核（`middleware/auth.go:536`、`middleware/distributor.go:48`、`controller/user.go:215`、`service/task_artifact_access.go`、`router/task-router.go`）。其余 [N4x] 线索**未**在本轮复核。

**C. 设计层面待定（需要在实现阶段定值，但不影响本文的确定性）**

10. 各保留期的具体天数、`AuthzStamp` 缓存 TTL 上限、邀请码默认有效期与 `max_uses` 默认值、`Retry-After` 的具体算法、`hide_results` 之外的 `scope` 组合是否需要新增——这些是**配置值**，取值确定后仍受本文规则约束。
11. **未核实**：本文给出的原子扣减 SQL（`UPDATE ... WHERE state='active' AND redeemed_count < max_uses`）在 SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6 三库上的并发与锁行为一致性。底座强制要求三库兼容（`docs/stage0/base-lock.md` §6），该语句与相关唯一索引**必须在三库上实测**，本文未做任何验证。
12. **未核实**：ASVS 5.0 **V7（Session Management）与 V2（Validation and Business Logic）**的具体条目号未逐条核对（V6 与 V16 的条目号已按原文核对）。§7 表中 V7/V2 行仅作章节级引用。
13. 本文**不声称**任何形式的合规、通过或验证结论。

---

## 11. 状态数与规则数小结（便于后续任务引用）

| 类别 | 数量 | 位置 |
|---|---|---|
| 对象状态机（枚举定义） | 12 个（Account / PlatformKey / ShareGroupVis / ShareGroupState / OfferingVis / OfferingState / Grant / InviteKind / InviteState / UpstreamKey / Channel / EmergencyScope） | §0.2 |
| 生命周期事件行 | 7 行（R1a、R1b、R1c、R2、R3、R4a、R4b） | §4.2 |
| 生命周期行为格 | 7 行 × 7 格 = **49 格**，每格均有确定动作与依据码 | §4.2 / §4.3 |
| 关键场景 | **10 个**（①–⑧ 为必列，⑨⑩ 为补充边界） | §8 |
| 规则编号 | R-HARD-01…06（含 a 变体）、R-PREF-01…03、R-PARAM-01a…d、R-GRANT-01…06、R-INV-01…05/10…13/20…26、R-SCOPE-01…03、R-LIFE-00…10、R-VER-01…02、R-CACHE-01…05、R-REVAL-01…10、R-ERR-01…32 | 全文 |
