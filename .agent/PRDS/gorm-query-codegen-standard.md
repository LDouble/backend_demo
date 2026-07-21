# Campus Platform: GORM Query 代码生成规范

**Priority:** High
**Status:** Done
**Type:** Enhancement
**Created:** 2026-07-21
**Last Updated:** 2026-07-21

## Overview

引入 GORM Gen，以手写核心模型为输入生成类型安全 Query，并改造现有 MySQL Repository 使用生成代码。将依赖方向、生成步骤和数据库变更流程写入仓库规范，供后续所有 DB 开发遵循。

## User Story

**As a** 后端开发者
**I want** 数据库字段和查询代码由固定版本生成器统一产生
**So that** 减少字符串字段、重复 CRUD 和数据库模型泄漏，同时维持核心层独立

## Context

现有 Repository 直接使用 GORM 字符串条件。核心模型已经是稳定的业务契约，因此不从数据库反向生成重复 Model，也不允许核心层依赖基础设施生成包。

## Implementation Overview

固定 `gorm.io/gen` 版本，新增可由 `go generate ./...` 调用的生成程序。输入仅为 `internal/core/model` 中明确登记的实体，输出到 `internal/infrastructure/mysql/query`。生成模式要求所有数据库操作显式传递 `context.Context`。Repository 对核心层接口负责，内部使用 `query.Use(db)` 和生成字段表达式。

## Features / Requirements

1. **生成链路**
   - 为 `User`、`Role`、`Config` 生成类型安全 Query
   - 生成文件带标准标记，禁止手工编辑
   - `make generate` 同时生成 OpenAPI 与 GORM Query，并检测未提交漂移

2. **Repository 改造**
   - 普通 CRUD、过滤、排序和分页使用生成 Query
   - 阻塞操作必须调用 `WithContext(ctx)`
   - 乐观锁、表达式更新等无法清晰表达的操作允许在 Repository 内使用 GORM，但必须使用生成字段名或集中封装并说明原因

3. **强制规范**
   - 领域实体和 Repository 接口属于核心层
   - 生成包只能由基础设施层引用，禁止 Handler、Service 和核心层直接引用
   - 新增表的固定顺序为迁移、核心实体与接口、登记生成、Repository、测试
   - 更新 `AGENTS.md` 与数据库开发文档，禁止手改生成文件

## Files to Create

- `internal/infrastructure/mysql/generator/main.go`：GORM Gen 配置入口
- `internal/infrastructure/mysql/query/*`：生成的类型安全查询代码
- `docs/database-development.md`：数据库开发与生成规范
- `internal/infrastructure/mysql/repository_test.go`：Repository 回归测试

## Files to Modify

- `go.mod`、`go.sum`：固定 GORM Gen 依赖
- `internal/infrastructure/mysql/*_repository.go`：使用生成 Query
- `Makefile`：统一生成和漂移检查
- `AGENTS.md`、`README.md`：固化开发规范与命令

## Database Changes

不新增或修改表结构。版本化 SQL 迁移仍是数据库结构的唯一事实来源，GORM Gen 不执行运行时迁移。

## Libraries/Dependencies

- `gorm.io/gen`：基于现有核心 Model 生成类型安全 DAO API
- `gorm.io/gorm`：Repository 运行时与事务支持

## Technical Implementation

### Architecture Approach

依赖方向固定为 `core Repository interface ← mysql Repository → generated Query`。生成器读取核心 Model，但运行时核心代码不读取生成包。生成产物使用 `Use(db)` 初始化，避免可变全局默认数据库。

### Technical Considerations

- 不启用 `WithoutContext`，所有查询显式绑定请求上下文
- 不从在线数据库生成，避免生成结果依赖个人数据库状态
- 不生成 Casbin 策略表，继续由 Casbin GORM Adapter 管理
- 迁移与 Model 不一致时，必须先修复漂移再提交生成结果

## Testing Requirements

### Unit Tests

- 使用 SQLite 验证用户、角色和配置 Repository 的生成 Query CRUD
- 验证分页、按用户名/角色名查询及配置乐观锁冲突

### Quality Gates

- 连续两次生成结果一致
- `go test -race ./...`、`go vet ./...`、lint 与构建通过
- 核心层及 API 层不存在对 MySQL 生成包的引用

---

**Implementation Notes:** 若未来接入既有遗留数据库，可单独执行一次数据库反向建模并人工评审；该流程不能替代版本化迁移，也不作为日常生成方式。
