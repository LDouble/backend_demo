# 数据库开发与代码生成规范

## 核心原则

- `migrations/*.sql` 是数据库结构的唯一事实来源，生产启动不执行 `AutoMigrate`。
- `internal/core/model` 保存手写核心实体，核心包定义 Repository 接口。
- `internal/infrastructure/mysql/query` 由 GORM Gen 生成，只能被 MySQL 基础设施层引用。
- API DTO 由 OpenAPI 生成，禁止直接序列化数据库或生成 Query 类型。

依赖方向必须保持：

```text
HTTP → Core Service → Repository Interface
                         ↑
MySQL Repository → Generated Query
```

## 标准开发流程

1. 新业务先定义 `schemas/<module>.yaml`，实体写入 `entities`，HTTP 能力写入 `operations`；执行 `campusctl module validate` 和 `campusctl generate module`。核心表变更可直接从下一步开始。
2. 执行 `campusctl migration plan <module>` 审核 Schema 与已 promote 快照的差异；破坏性变化和重命名必须显式确认，其中重命名需在 Schema 声明 `rename_from`。
3. 执行 `campusctl migration new <name> --module <module>` 创建 `migrations/drafts/` 下的 up/down 草案，审核并补全 SQL 后执行 `campusctl migration promote <draft>`。正式迁移使用 UTC `YYYYMMDDHHMMSS` 版本，promote 不执行数据库迁移。
4. 更新核心实体，或审核模块生成实体及 Repository 契约。
5. 核心实体加入共享 GORM Gen；业务模块的生成 Repository 仅承载无业务规则的基础 CRUD，复杂查询必须接入类型安全 Query。
6. 执行 `make generate` 刷新模块、全局 OpenAPI、oapi-codegen 和 GORM Query，再执行 `make generate-check` 检查漂移。
7. 添加 Repository 回归测试；依赖 MySQL 行为时增加带 `integration` 构建标签的测试。
8. 执行 `make migration-check`、`make check-architecture`、`make test-generator`、`make test-race`、`make vet`、`make lint` 和 `make build`。

## 查询约束

每个阻塞调用都必须绑定上下文：

```go
row, err := repository.query.User.WithContext(ctx).
	Where(repository.query.User.Username.Eq(username)).
	First()
```

普通查询必须使用生成字段的 `Eq`、`In`、`Order`、`UpdateSimple` 等方法，不得写 `"username = ?"` 或 `"id ASC"`。Repository 使用 `query.Use(db)` 创建实例，不调用可变的全局 `query.SetDefault`。

事务、行锁、数据库函数或复杂聚合若无法由生成 API 清晰表达，可以在 Repository 内通过 `UnderlyingDB` 或专用 SQL 实现，但必须：

- 继续传递 `context.Context`；
- 使用参数绑定，禁止拼接外部输入；
- 在代码中说明例外原因；
- 添加针对该数据库行为的集成测试。

通知模块的受众快照、乐观锁更新和 Outbox 租约属于上述例外：它们必须在同一事务内跨多表执行，并使用 `FOR UPDATE SKIP LOCKED`。普通单表 CRUD 仍由生成 Repository/GORM Query 承担。

## 生成代码管理

生成文件包含 `DO NOT EDIT` 标记。修改核心 Model 或升级 GORM Gen 后必须重新执行：

```bash
make generate
```

生成过程不连接开发者数据库，因此相同源码应产生相同结果。既有遗留数据库的反向建模只能作为一次性导入手段，完成后必须补齐基线迁移，不能成为日常开发流程。

模块生成器同样不得连接数据库，也不得在普通 `make generate` 中创建正式迁移。迁移草案只能由 `campusctl migration new` 写入 `migrations/drafts/`，生产迁移器只读取 `migrations/` 根目录的正式 up/down 文件。最后一次 promote 的规范化 Schema 保存在 `.agent/schema-snapshots/`，正式文件及 checksum 记录在 `migrations/manifest.json`。

提交前运行 `campusctl migration check`（或 `make migration-check`），只读检查版本唯一性、up/down 配对、时间戳、checksum、遗留草案及 Schema 漂移。既有模块首次接入使用 `campusctl migration snapshot <module>` 建立基线，不生成重复建表迁移。

模块 API、权限和 HTTP 适配流程见 [`module-development.md`](module-development.md)。数据库字段或索引变更与 API operation 变更可以位于同一 Schema，但编号迁移仍须人工审核。
