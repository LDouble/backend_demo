# Campus Platform: 阶段 2 Schema 生成器与开发规范

**Priority:** High
**Status:** Done
**Type:** Feature
**Created:** 2026-07-21
**Last Updated:** 2026-07-21

## Overview

在阶段 1 的 OpenAPI 与 GORM Query 生成能力之上，增加统一的业务模块 Schema、可重复执行的模块生成器和 `campusctl` 管理入口，使新增业务遵循“定义 Schema、生成基础代码、补充领域规则、测试”的固定流程。

## User Story

**As a** 平台后端开发者或编码 Agent
**I want** 通过一份声明式 Schema 生成模块骨架和基础契约
**So that** 可以减少重复 CRUD 编码，并将人工修改集中在稳定的领域扩展点

## Implementation Overview

使用 Go 标准库 `text/template`、`embed` 与现有 `yaml.v3` 实现 `internal/generator`。Schema 经过解析、语义校验和规范化后生成确定性文件；带生成标记的文件允许覆盖，领域规则与扩展文件只在首次生成时创建。`campusctl` 负责校验、生成、检查漂移和维护模块清单，不在生成器中直接连接生产数据库。

## Features / Requirements

1. **模块 Schema**
   - 定义模块、实体、表名、字段、索引、CRUD 开关和 API 权限
   - 校验 Go 标识符、SQL 名称、字段类型、重复字段和危险输出路径
   - 提供 `schemas/examples/activity.yaml` 作为可执行示例

2. **确定性代码生成**
   - 生成领域实体、Repository 契约、GORM 基础设施、Service、Handler 骨架、OpenAPI 片段、迁移 SQL 和权限清单
   - 生成文件包含来源与“禁止手改”标记，输出顺序和格式稳定
   - `rule.go`、自定义扩展和测试骨架只创建一次，重新生成不得覆盖

3. **campusctl 工作流**
   - `campusctl module validate <schema>` 校验并输出规范化摘要
   - `campusctl generate module <schema>` 生成或更新模块
   - `campusctl generate module <schema> --check` 检测生成漂移但不写文件
   - `campusctl module list` 读取 `.agent/modules.json` 展示模块

4. **AI 开发规范**
   - 新增 `.agent/architecture.md`、`.agent/rules.md` 和 `.agent/modules.json`
   - 明确依赖方向、生成文件边界、数据库变更顺序和必跑验证命令
   - 更新 `AGENTS.md`、README 与数据库开发规范

## Files to Create

- `internal/generator/`：Schema、校验、规划、渲染与模板
- `schemas/examples/activity.yaml`：示例模块定义
- `.agent/architecture.md`、`.agent/rules.md`、`.agent/modules.json`：Agent 上下文
- `internal/generator/*_test.go`：表驱动、快照与安全测试

## Files to Modify

- `cmd/campusctl/main.go`：增加 module/generate 命令并拆分可测 CLI 逻辑
- `Makefile`：增加生成、漂移检查和生成器测试命令
- `README.md`、`AGENTS.md`、`docs/database-development.md`：固化新流程

## Generated Output Contract

默认输出到 `internal/modules/<module>/`、`api/modules/<module>.yaml`、`migrations/modules/` 和 `permissions/modules/`。生成器不得直接修改阶段 1 的全局 OpenAPI 文件或已执行的历史迁移；全局契约合并与迁移编号由显式命令规划并在写入前检查冲突。

## Libraries/Dependencies

- Go `text/template`、`embed`、`go/format`：本地、可复现模板生成
- `gopkg.in/yaml.v3`：Schema 解析
- GORM Gen：继续承担数据库类型安全 Query 生成
- oapi-codegen：继续从 OpenAPI 3.0 契约生成 Gin DTO 与路由

## Technical Considerations

- 所有输出路径必须限制在仓库根目录内，拒绝绝对路径及 `..` 穿越
- 先在内存中完成全部渲染和格式化，成功后再原子写入，避免半生成状态
- `--check` 比较内容而非时间戳，便于 CI 检测漂移
- 模板和 Schema 必须版本化；未知版本立即失败
- 模块生成不自动注册路由或执行迁移，避免隐式修改运行系统

## Testing Requirements

### Unit Tests

- 表驱动覆盖 Schema 正常化、类型映射、命名校验、重复项和路径穿越
- 验证重复生成字节一致、手写扩展不被覆盖、模板输出可通过 `go/format`
- 覆盖 CLI 参数错误、校验、生成、漂移检查和模块列表

### Integration Tests

- 从 activity 示例生成到临时目录并执行 `go test`/OpenAPI 解析
- 修改 Schema 后 `--check` 返回漂移，重新生成后恢复通过
- 生成迁移可在隔离 MySQL 中执行 `up → down → up`

### Acceptance

- `go generate ./...` 结果稳定
- `go test -race ./...`、`go vet ./...`、`golangci-lint run ./...`、`go build ./...` 全部通过
- 生成器核心包覆盖率不低于 80%

---

**Implementation Notes:** 阶段 2 只交付生成基础设施和规范，不实现活动等业务功能，也不自动运行迁移或改写人工业务规则。
