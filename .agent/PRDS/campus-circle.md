# 校园圈与可配置子模块

**Priority:** High
**Status:** Approved
**Type:** Feature
**Created:** 2026-07-23
**Last Updated:** 2026-07-23

## Problem

活动、二手、跑腿和拼车承载结构化业务，但平台缺少校园日常、失物招领、校园问答、经验分享和兴趣交流等通用内容场景。如果每类内容都实现为独立业务模块，发布、审核、互动和后台管理会产生大量重复逻辑。同时，当前内容发布规则需要统一收紧为“有效教务认证后才可发布”，避免游客账号直接生产公开内容。

## Goal

交付一个支持后台动态配置两级子模块的校园圈，让已完成教务认证的用户发布图文帖子，帖子和评论先审后发，并将“所有用户内容必须认证后才能发布”落实为跨模块统一契约。

## Scope

### In

- 校园圈一级频道、二级子版块的后台配置、排序、启用和归档。
- 校园圈图文帖的创建、编辑、本人列表、撤回、重新提交和公开信息流。
- 帖子审核通过、拒绝和撤销审核结果。
- 帖子点赞、取消点赞及查看者关系和可用 Action。
- 复用通用评论能力，支持先审后发、多级回复和帖主置顶。
- 活动、二手、跑腿、拼车、校园圈帖子、评论及回复统一要求有效教务认证后才能创建或重新提交审核。
- 所有请求、响应、枚举、Action 和错误响应均在源 Schema 中明确定义并生成 Go 类型。
- 乐观锁、幂等控制、领域事件和 Repository 可见性约束。

### Out of Scope

- 匿名发布、匿名评论。
- 用户自行创建圈子、私密圈子和付费圈子。
- 无限层级子模块；第一期最多支持一级频道和二级子版块。
- 视频转码、推荐算法、热榜、关注用户。
- 举报、封禁和敏感词平台。
- 管理员置顶帖子。

## Product Rules

### Academic verification gate

- 有效教务认证是所有用户内容创建和送审的前置条件。
- 适用内容包括活动、二手商品、跑腿、拼车、校园圈帖子、根评论和回复。
- 未认证用户可以读取按现有可见性规则公开的内容。
- 未认证用户不得创建新内容，也不得把草稿或被拒绝内容提交/重新提交审核。
- 用户仍可查看、编辑和撤回自己已经存在的内容；编辑不等于获得发布资格。
- 认证失效不会自动隐藏此前已经审核通过的内容，但用户再次提交内容前必须恢复有效认证。
- 管理员代运营发布不受普通用户门槛影响，仍受 Casbin 管理权限、OpenAPI 校验和审计约束。
- 被门槛阻止的接口统一返回 `403 academic_verification_required`。
- 资源响应中的 `available_actions` 对未认证用户只返回 `verify_academic`，不得同时暴露不可执行的发布或互动 Action。

### Section hierarchy

- 一级频道的 `parent_id` 为空，二级子版块指向一个一级频道。
- 帖子只能发布到启用的叶子版块。
- 最大深度为两级，禁止循环引用。
- `slug` 全局唯一且创建后不可修改。
- 存在子节点或历史帖子的版块不能物理删除，只能归档。
- 归档版块不再接受新帖子，历史审核通过帖子仍可读取。
- 父频道归档后，所有子版块停止接收新帖子。

### Post moderation and visibility

| Status | Author | Other users | Admin |
|---|---:|---:|---:|
| `pending_review` | visible | hidden | visible |
| `approved` | visible | visible | visible |
| `rejected` | visible | hidden | visible |
| `withdrawn` | visible | hidden | visible |

- 新帖子直接进入 `pending_review`。
- 审核拒绝必须保存原因。
- 编辑 `approved` 或 `rejected` 帖子后清除原审核结果并进入 `pending_review`。
- 管理员撤销审核后，帖子立即回到 `pending_review` 并停止公开展示。
- 不可见帖子通过详情、评论和点赞入口均表现为不存在，避免泄露资源状态。

### Interaction

- 已认证用户可以点赞或评论其他用户审核通过的帖子。
- 点赞和取消点赞固有幂等。
- 校园圈帖子作为通用评论目标 `campus_circle_post` 接入。
- 评论继续遵循自己的审核生命周期；帖子作者可以置顶一条审核通过的根评论。

## Acceptance Criteria (EARS)

- WHEN 未认证用户创建活动、二手商品、跑腿、拼车、校园圈帖子、评论或回复, THE SYSTEM SHALL 返回 `403 academic_verification_required` 且不写入业务数据或幂等成功快照。
- WHEN 未认证用户提交或重新提交任一用户内容审核, THE SYSTEM SHALL 返回 `403 academic_verification_required`。
- WHEN 未认证用户查看允许公开读取的内容, THE SYSTEM SHALL 保持现有可见性行为。
- WHEN 用户认证失效, THE SYSTEM SHALL 保留其此前审核通过内容的公开状态；WHEN 该用户再次创建或送审内容, THE SYSTEM SHALL 要求恢复认证。
- WHEN 管理员创建一级频道和二级子版块, THE SYSTEM SHALL 返回按后台顺序组织的完整频道树。
- IF 子模块超过两级、形成循环或使用重复 `slug`, THEN THE SYSTEM SHALL 拒绝请求且不写入部分数据。
- WHEN 已认证用户向启用的叶子版块发布帖子, THE SYSTEM SHALL 创建 `pending_review` 帖子。
- IF 用户向一级频道、归档版块或归档父频道下的版块发布帖子, THEN THE SYSTEM SHALL 拒绝请求。
- WHILE 帖子处于 `pending_review` 或 `rejected`, THE SYSTEM SHALL 只允许作者和管理员读取。
- WHEN 管理员审核通过帖子, THE SYSTEM SHALL 将其展示到公开信息流并记录审核人、时间和版本。
- WHEN 作者编辑已通过或已拒绝的帖子, THE SYSTEM SHALL 清除旧审核结果并重新进入待审核。
- WHEN 作者撤回帖子, THE SYSTEM SHALL 从公开信息流、点赞入口和评论目标解析中隐藏该帖子。
- WHEN 管理员撤销审核结果, THE SYSTEM SHALL 将帖子恢复为待审核并立即停止公开展示。
- WHEN 已认证用户重复点赞同一帖子, THE SYSTEM SHALL 只保留一条点赞记录并返回一致结果。
- WHEN 校园圈帖子接入评论, THE SYSTEM SHALL 支持先审后发、多级回复和帖主置顶。
- IF 请求携带过期 `expected_version`, THEN THE SYSTEM SHALL 返回 `409 version_conflict` 且不覆盖新版本。
- WHEN OpenAPI 生成完成, THE SYSTEM SHALL 为全部校园圈请求、响应、状态枚举和 Action 生成明确的 Go 类型。

## Technical Design

### Module boundary

新增生成式模块 `campus_circle`。子模块是校园圈领域内动态配置的版块数据，不为每个子模块生成独立 Go package。模块 Schema 是 operation、OpenAPI、DTO、Casbin 权限和 HTTP adapter 的唯一事实来源。

### Data model

`campus_circle_sections`:

- `id`, `parent_id`, `slug`, `name`, `description`
- `icon_url`, `cover_url`, `sort_order`, `status`
- `version`, `created_by`, `updated_by`, `created_at`, `updated_at`
- `UNIQUE(slug)`
- `INDEX(parent_id, status, sort_order)`

`campus_circle_posts`:

- `id`, `section_id`, `author_id`, `title`, `content`, `status`
- `review_reason`, `reviewed_by`, `reviewed_at`, `published_at`
- `version`, `created_at`, `updated_at`
- `INDEX(status, section_id, published_at, id)`
- `INDEX(author_id, status, created_at, id)`

`campus_circle_post_images`:

- `id`, `post_id`, `url`, `sort_order`
- `UNIQUE(post_id, sort_order)`

`campus_circle_post_likes`:

- `post_id`, `user_id`, `created_at`
- `UNIQUE(post_id, user_id)`
- `INDEX(user_id, created_at)`

标题可选且最多 100 个 Unicode 字符，正文最多 5000 个字符，图片最多 9 张；标题、正文和图片不得同时为空。

### Publishing gate

复用模块生成器已经支持的 `academic_verification: required|none` operation 声明。所有用户内容创建、提交审核和重新提交审核 operation 标记为 `required`，校验保持在 Typed Params 之后、幂等事务之前执行，避免 Handler 重复查询和未认证失败结果进入幂等响应快照。

管理员审核、撤销审核和版块配置操作使用管理权限，不声明普通用户教务认证门槛。

### API contracts

用户接口：

- `GET /campus-circle/sections`
- `GET /campus-circle/posts`
- `POST /campus-circle/posts`
- `GET /campus-circle/posts/{postId}`
- `PUT /campus-circle/posts/{postId}`
- `GET /campus-circle/posts/mine`
- `POST /campus-circle/posts/{postId}/submit-review`
- `POST /campus-circle/posts/{postId}/withdraw`
- `PUT /campus-circle/posts/{postId}/like`
- `DELETE /campus-circle/posts/{postId}/like`

管理接口：

- `GET|POST /admin/campus-circle/sections`
- `PUT /admin/campus-circle/sections/{sectionId}`
- `POST /admin/campus-circle/sections/{sectionId}/archive`
- `POST /admin/campus-circle/sections/{sectionId}/activate`
- `GET /admin/campus-circle/posts`
- `GET /admin/campus-circle/posts/{postId}`
- `POST /admin/campus-circle/posts/{postId}/review`
- `POST /admin/campus-circle/posts/{postId}/revoke-review`

用户响应包含 `viewer_relation`, `liked`, `like_count`, `comment_count` 和 `available_actions`。其中 `comment_count` 表示审核通过且公开可见的根评论数量，与根评论分页的 `total` 语义一致；列表派生数据使用批量聚合，禁止 N+1。

### Concurrency and events

编辑、撤回、提交审核、审核和撤销审核均使用 `expected_version`。事务内锁定聚合、校验状态和版本、更新记录并写 Domain Event/Outbox。

事件包括：

- `campus_circle.post_created`
- `campus_circle.post_updated`
- `campus_circle.review_submitted`
- `campus_circle.post_reviewed`
- `campus_circle.review_revoked`
- `campus_circle.post_withdrawn`
- `campus_circle.post_liked`
- `campus_circle.post_unliked`

## Verification

- 领域与应用层使用表驱动测试覆盖认证门槛、层级、状态转换、权限和边界。
- Repository 测试覆盖公开/本人/管理员可见性、事务回滚、唯一约束和批量派生字段。
- `campus_circle/domain` 和 `campus_circle/application` 目标覆盖率不低于 90%。
- `campus_circle/infrastructure` 目标覆盖率不低于 80%。
- 目标检查包括 `gofmt`、目标包 `go test`、`make generate-check`、`make migration-check`、`make check-architecture` 和 `git diff --check`。
- PR CI 执行仓库规范要求的完整检查和 Compose 验收。

## Delivery Slices

1. 全局发布认证门槛：补齐现有活动、二手、跑腿、拼车和评论的 operation 声明与回归测试。
2. 校园圈基础闭环：子模块、帖子、本人列表、公开列表、审核和撤销审核。
3. 校园圈互动闭环：点赞、Action、通用评论目标接入和批量计数。
4. 管理端闭环：同步生成客户端，实现版块配置与帖子审核页面并完成 Docker 真实 API 联调。
5. 小程序闭环：频道信息流、发布、详情和我的发布。

## Dependencies

- 教务认证与 `guest/member` 权限分层已交付。
- 通用评论、评论审核、多级回复和帖主置顶已交付。
- 管理端必须从最终 `api/openapi.yaml` 同步生成客户端。
