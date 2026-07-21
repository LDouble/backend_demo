# Campus Platform: 阶段 1 基础闭环

**Priority:** High
**Status:** Done
**Type:** Feature
**Created:** 2026-07-21
**Last Updated:** 2026-07-21

## Overview

从设计文档搭建可运行的校园平台后端基础，为后续业务模块提供统一的认证、授权、配置与基础设施能力。

## User Story

**As a** 平台管理员
**I want** 使用安全的账号登录并管理用户、角色、权限和运行配置
**So that** 后续校园业务模块可以复用稳定的基础能力快速接入

## Implementation Overview

使用 Go 1.25、Gin、GORM、MySQL、go-redis、JWT、Casbin 和 Zap。采用 OpenAPI First，通过 oapi-codegen 生成 HTTP DTO、ServerInterface 和 Gin 路由注册；认证、鉴权、日志、恢复及请求 ID 使用中间件。HTTP 实现只保留业务编排，核心包通过小接口依赖 MySQL、Redis 与 Casbin；阻塞调用均接收 `context.Context`。

## Features / Requirements

1. **认证与会话**
   - bcrypt cost 12 存储密码
   - 15 分钟 Access Token 与 7 天轮换 Refresh Token
   - Redis 保存多设备会话，支持单会话登出

2. **用户与权限**
   - 管理员创建、更新、启用或禁用用户
   - 管理角色、用户角色及基于 URL 和 HTTP 方法的 Casbin 权限
   - 保护最后一个超级管理员

3. **配置中心**
   - 普通与 AES-256-GCM 加密配置
   - 敏感值只写、更新使用版本乐观锁

4. **可运维性**
   - 版本化迁移、管理员初始化、健康检查、JSON 日志和优雅停机
   - Docker Compose 启动 API、MySQL、Redis 和迁移任务

## API Endpoints

- `GET /health/live`、`GET /health/ready`
- `POST /api/v1/auth/login|refresh|logout`、`GET /api/v1/auth/me`
- `/api/v1/users` 用户管理 API
- `/api/v1/roles` 角色与授权管理 API
- `/api/v1/configs` 配置管理 API

## Database Changes

- `users`：账号、密码摘要、状态与时间戳
- `roles`：角色名称、描述及内置标记
- `configs`：分组、键、值、加密标记、版本和更新人
- `casbin_rule`：Casbin GORM Adapter 持久化策略

## 验收补齐范围

- Handler 自动覆盖绑定错误、未认证、无权限、禁用用户、重复用户名、版本冲突和统一响应
- 真实 Redis 自动覆盖 TTL、原子轮换、登出删除和会话失效
- 隔离 MySQL 数据库自动验证迁移 `up → down → up`
- Compose 自动验证权限即时生效、Casbin 重载和数据重启持久化
- 真实 MySQL 原始列验证加密配置不含明文
- 每个有业务语句的核心包覆盖率达到 80%

## Libraries/Dependencies

- Gin v1.12.0、GORM v1.31.2、MySQL Driver v1.6.0
- go-redis v9.21.0、golang-jwt v5.3.1
- Casbin v3.10.0、GORM Adapter v3.41.0
- Zap v1.28.0、golang-migrate v4.19.1、x/crypto v0.54.0
- oapi-codegen v2.8.0：生成 OpenAPI DTO 与 Gin Server 路由

## Testing Requirements

### Unit Tests

- 密码、JWT、会话轮换和 AES-GCM 正常及失败场景
- 用户状态、配置乐观锁及超级管理员保护
- HTTP 绑定、认证、鉴权和统一错误响应

### Integration Tests

- MySQL 迁移与 Casbin 策略持久化
- Redis 会话过期、轮换与登出
- 加密配置数据库中不出现明文

### Manual Testing Checklist

- 全新 Compose 环境可迁移并启动
- 初始化管理员可登录并管理资源
- 权限变更即时生效且服务重启后保留
- 竞态检查、静态检查、构建与核心测试通过

---

**Implementation Notes:** 微信接入、业务模块、通用业务代码生成器、Asynq 及完整可观测性栈不属于阶段 1；OpenAPI 与 GORM Query 生成流程已纳入基础规范。
