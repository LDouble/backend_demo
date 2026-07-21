# Campus Platform: 通知中心与多渠道投递

**Priority:** High
**Status:** Complete
**Type:** Feature
**Created:** 2026-07-21
**Last Updated:** 2026-07-21

## Overview

交付管理员通知管理、全员/角色/用户发布时快照、用户收件箱与已读状态，并以 MySQL Outbox、Asynq 和可替换 Provider 建立至少一次外部投递闭环。

## User Story

**As a** 平台管理员和校园用户
**I want** 定时或立即发布通知并可靠查看个人收件箱
**So that** 校园消息能够可审计、可重试且为后续微信推送复用

## Implementation Overview

扩展兼容 v1 的多实体 Schema v2，生成通知模块多表实体和 GORM Query 登记。领域层实现状态机、受众快照、乐观锁和已读规则；API 继续采用 OpenAPI First、JWT 与 Casbin。Worker 使用独立 Redis DB、Outbox 租约和 Asynq 重试，首版日志 Provider 不发送真实微信消息。

## Requirements

- 状态为 `draft/scheduled/publishing/published/revoked`，发布后内容不可修改
- 通知支持 Markdown 正文、分类、优先级、跳转路径、站内与 push 通道
- 实际发布时快照启用用户；显式禁用或不存在用户导致请求失败
- `member` 内置角色自动分配并拥有个人通知权限
- 定时发布、撤回、失败投递查询与重试均可审计

## API Endpoints

- 管理端：`/api/v1/admin/notices` CRUD、publish、revoke、deliveries、retry
- 用户端：`/api/v1/notices` 列表、详情、未读数、单条和全部已读

## Database Changes

- `notices`、`notice_audiences`、`notice_recipients`
- `notice_deliveries`、`outbox_events`

## Testing Requirements

- 生成器 v1/v2、状态机、乐观锁、快照去重、已读幂等和撤回
- MySQL 迁移、Outbox 租约、Asynq 重试、权限与 API 集成测试
- Race、Vet、Lint、Build、覆盖率和 Compose 验收全部通过

---

**Implementation Notes:** 不实现真实微信、邮件、短信、附件、前端和免打扰设置。
