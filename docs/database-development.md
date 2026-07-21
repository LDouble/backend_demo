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

1. 新业务先定义 `schemas/<module>.yaml`，执行 `campusctl module validate` 和 `campusctl generate module`；核心表变更可直接从下一步开始。
2. 审核 `migrations/modules/` 的生成草案，将其改为 `migrations/` 下成对且连续编号的 `*.up.sql` 与 `*.down.sql`。
3. 更新核心实体，或审核模块生成实体及 Repository 契约。
4. 核心实体加入共享 GORM Gen；业务模块的生成 Repository 仅承载无业务规则的基础 CRUD，复杂查询必须接入类型安全 Query。
5. 执行 `make generate-check`，提交 Schema 与生成结果。
6. 添加 Repository 回归测试；依赖 MySQL 行为时增加带 `integration` 构建标签的测试。
7. 执行 `make check-architecture`、`make test-generator`、`make test-race`、`make vet`、`make lint` 和 `make build`。

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

## 生成代码管理

生成文件包含 `DO NOT EDIT` 标记。修改核心 Model 或升级 GORM Gen 后必须重新执行：

```bash
make generate
```

生成过程不连接开发者数据库，因此相同源码应产生相同结果。既有遗留数据库的反向建模只能作为一次性导入手段，完成后必须补齐基线迁移，不能成为日常开发流程。

模块生成器同样不得连接数据库。模块 Schema 生成的 SQL 位于 `migrations/modules/`，只是待审核草案；不得直接由生产迁移器执行。生成器只覆盖带生成标记的文件，`domain/rule.go` 等人工扩展点必须保留。
