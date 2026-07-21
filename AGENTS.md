# Repository Guidelines

## 项目结构与职责

服务入口位于 `cmd/server` 和 `cmd/campusctl`。业务实体、服务及 Repository 接口位于 `internal/core`；Gin Handler、中间件和 OpenAPI 生成代码位于 `internal/api`；MySQL、Redis、迁移和日志适配器位于 `internal/infrastructure`。SQL 迁移存放在 `migrations/`，公开 API 契约为 `api/openapi.yaml`，任务与 PRD 存放在 `.agent/`。

## 构建与验证

```bash
make generate-check      # 检查 OpenAPI、GORM Query 与模块生成漂移
make test-race           # 运行竞态和覆盖率测试
make test-core-coverage  # 强制每个核心业务包覆盖率不低于 80%
make test-generator      # 验证 Schema 生成器及其覆盖率门槛
make test-compose        # 完整 MySQL/Redis/重启验收并自动清理
make vet                 # Go 静态检查
make lint                # golangci-lint
make build               # 构建全部命令
make env && make compose-up
```

提交前还需执行 `make check-architecture` 和 `git diff --check`。

## 数据库开发规范

版本化 SQL 迁移是表结构的唯一事实来源，禁止在运行时调用 `AutoMigrate`。领域实体和 Repository 接口由核心层手写；GORM Gen 只基于这些实体生成 `internal/infrastructure/mysql/query`，禁止手工修改生成文件，也禁止 `internal/core` 或 `internal/api` 引用该包。

新增或修改 DB 逻辑必须按以下顺序：

1. 编写可升降级迁移。
2. 更新核心实体及 Repository 接口。
3. 在 `mysql/generator/main.go` 登记实体并执行 `make generate`。
4. 在 MySQL Repository 内使用生成字段与 Query，实现核心接口。
5. 添加 Repository 回归测试和必要的 MySQL 集成测试。

所有查询必须使用 `WithContext(ctx)`。普通过滤、排序、分页和更新禁止使用字符串列名；特殊锁、数据库表达式或复杂 SQL 只能封装在 Repository 内，并注释说明生成 API 无法表达的原因。Casbin 策略表由其 Adapter 管理，不纳入生成。

## 模块生成规范

新增业务模块先定义 `schemas/<module>.yaml`，再执行 `campusctl module validate` 和 `campusctl generate module`。v1 用于单实体兼容，新增多实体模块使用 v2，并声明联合索引、主实体及外键依赖。允许重新生成 `.gen.go`、模块 OpenAPI、权限清单和迁移草案；禁止覆盖 `domain/rule.go` 等人工扩展点。生成迁移经评审、编号后才能移入主迁移序列，生成器不得自动执行迁移或注册路由。

## Go 风格与测试

代码必须通过 `gofmt`，包名使用简短小写单词，导出标识符使用 `PascalCase`，文件名使用小写蛇形。优先编写表驱动测试，覆盖成功、边界、权限失败、乐观锁和基础设施错误。生成代码不得直接作为 HTTP 响应；API DTO 由 OpenAPI 生成并由 Handler 显式映射。

## 安全与提交

禁止提交 `.env`、真实密钥或生产地址。提交建议遵循 Conventional Commits，例如 `feat(db): add activity repository`。PR 必须说明迁移、生成文件、配置影响和实际执行的验证命令。
