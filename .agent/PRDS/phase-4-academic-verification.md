# 阶段 4：教务认证与 Guest/Member 权限分层

**Priority:** High
**Status:** In Progress
**Type:** Feature
**Created:** 2026-07-22
**Last Updated:** 2026-07-22

## Problem

当前账号创建后立即获得 `member` 权限，无法把公开浏览者与已经核验的在校成员区分开，也缺少安全、可审计的学生证人工审核和教务凭据同步验证能力。

## Goal

让新账号以 `guest` 身份安全浏览公开业务内容，并且仅在教务身份验证成功后原子升级为 `member`；同时交付人工审核、Mock 同步验证、材料私有存储、撤销与清理闭环。

## Scope

- 内置 `guest/member` 基础角色互斥，通用角色管理不得直接改变基础角色。
- 业务读权限向 `guest/member` 开放，普通业务写权限仅向 `member` 开放。
- 生成式 `academic_verification: required|none` 写操作门槛。
- 本人认证状态、学生证材料上传/提交、教务凭据同步验证。
- 管理端申请列表、详情、受控材料读取、批准、驳回和身份撤销。
- AES-256-GCM 私有材料存储、签名校验、保留期清理和 Mock 白名单 Provider。
- 本人密码修改与全部会话撤销。
- 认证生命周期领域事件及不含敏感数据的站内通知。

## Out of Scope

- 公开注册、真实教务爬虫、外部对象存储、单独自助解绑。
- 历史账号角色回填；Phase 4 staging 允许重建。

## Acceptance Criteria (EARS)

- WHEN 管理员创建账号, THE SYSTEM SHALL 仅分配 `guest` 基础角色。
- WHEN 未认证用户读取公开活动、拼车、跑腿、已发布商品、本人通知或认证状态, THE SYSTEM SHALL 按资源可见性返回内容；WHEN 匿名用户请求任一业务接口, THE SYSTEM SHALL 返回 401。
- WHEN `guest` 执行业务写操作, THE SYSTEM SHALL 返回 403；WHEN 已认证 `member` 执行被授权写操作, THE SYSTEM SHALL 允许继续处理。
- WHERE 写 operation 声明 `academic_verification: required`, THE SYSTEM SHALL 在 Typed Params 后、幂等事务前校验有效教务身份，且自定义 Casbin 角色不得绕过。
- WHEN 用户修改本人密码并提供正确当前密码、新密码和幂等键, THE SYSTEM SHALL 更新 bcrypt 摘要、撤销全部会话并要求重新登录。
- WHEN 用户上传学生证, THE SYSTEM SHALL 仅接受签名匹配的 JPEG、PNG 或 WebP，限制 5 MiB，以 AES-256-GCM、随机存储键和 `0600` 权限保存，并返回一次性 `material_id`。
- WHEN 用户提交人工申请, THE SYSTEM SHALL 原子占用本人未过期材料并创建 `pending` 申请；IF 材料已占用、过期或属于其他用户, THE SYSTEM SHALL 拒绝。
- WHEN 管理员批准 pending 申请且版本匹配, THE SYSTEM SHALL 原子更新身份、supersede 其他 pending 申请、切换为 `member` 并写领域事件。
- WHEN 管理员驳回申请或撤销身份, THE SYSTEM SHALL 要求非空原因与匹配版本；WHEN 撤销成功, THE SYSTEM SHALL 原子切回 `guest`。
- WHEN 用户提交教务凭据, THE SYSTEM SHALL 在十秒内调用隔离 Provider，且密码不得进入数据库、事件、通知、审计日志或错误文本。
- WHEN 凭据连续失败达到用户、学号摘要或 IP 任一 15 分钟五次阈值, THE SYSTEM SHALL 返回 429；WHEN Provider 不可用, THE SYSTEM SHALL 返回 503。
- WHEN 新身份验证失败或换绑仍在审核, THE SYSTEM SHALL 保留旧身份和 `member`；WHEN新身份成功, THE SYSTEM SHALL 原子替换旧身份并维持学号全局唯一。
- WHEN 未绑定材料超过 24 小时或已完成审核材料超过 30 天, THE SYSTEM SHALL 由 Worker 删除密文文件并保留摘要和审核元数据。
- WHEN 认证批准、驳回、凭据成功或撤销事件被投递, THE SYSTEM SHALL 创建幂等站内通知，且载荷仅含用户 ID、申请 ID、状态和跳转路径。

## Technical Notes

- 版本化 SQL 迁移是三张认证表的唯一结构事实来源。
- Provider、材料存储和失败限流均通过小接口注入，便于测试和未来替换真实实现。
- API 与 Worker 共享受限材料卷；不生成静态 URL，管理读取响应使用 `Cache-Control: no-store`。
- 学号只在认证数据库中持久化，不进入领域事件或通知；限流键使用不可逆摘要。
