# Repository Guidelines

## 项目结构与职责

服务入口位于 `cmd/server` 和 `cmd/campusctl`。业务实体、服务及 Repository 接口位于 `internal/core`；Gin Handler、中间件和 OpenAPI 生成代码位于 `internal/api`；MySQL、Redis、迁移和日志适配器位于 `internal/infrastructure`。SQL 迁移存放在 `migrations/`，core API 与共享组件源契约为 `api/openapi.base.yaml`，生成后的公开 API 契约为 `api/openapi.yaml`，任务与 PRD 存放在 `.agent/`。

## 构建与验证

```bash
make generate-check      # 检查 OpenAPI、GORM Query 与模块生成漂移
make migration-check     # 只读检查迁移配对、checksum、草案及 Schema 漂移
make test-race           # 运行竞态和覆盖率测试
make test-core-coverage  # 强制每个核心业务包覆盖率不低于 80%
make test-generator      # 验证 Schema 生成器及其覆盖率门槛
make test-compose        # 完整 MySQL/Redis/重启验收并自动清理
make vet                 # Go 静态检查
make lint                # golangci-lint
make build               # 构建全部命令
make env && make compose-up
```

本地开发和提交前默认只执行轻量、与改动直接相关的检查：所有 Go 改动必须执行 `gofmt`，所有提交必须执行 `git diff --check`；根据改动范围运行目标包的 `go test`，涉及生成或迁移时分别运行 `make generate-check`、`make migration-check`，涉及分层依赖时运行 `make check-architecture`。本地无需默认执行完整的 race、覆盖率、generator、vet、lint、build 或 MySQL/Redis Compose 验收；这些完整检查统一由 PR CI 执行。只有定位 CI 失败，或改动无法由轻量检查充分验证时，才在本地补跑对应命令。

## 数据库开发规范

版本化 SQL 迁移是表结构的唯一事实来源，禁止在运行时调用 `AutoMigrate`。领域实体和 Repository 接口由核心层手写；GORM Gen 只基于这些实体生成 `internal/infrastructure/mysql/query`，禁止手工修改生成文件，也禁止 `internal/core` 或 `internal/api` 引用该包。

新增或修改 DB 逻辑必须按以下顺序：

1. 使用 `campusctl migration plan <module>` 比较当前 Schema 与快照。
2. 使用 `migration new` 创建 `migrations/drafts/` 草案，审核后用 `migration promote` 分配 UTC 时间戳版本；不得覆盖已 promote 文件。
3. 更新核心实体及 Repository 接口。
4. 在 `mysql/generator/main.go` 登记实体并执行 `make generate`。
5. 在 MySQL Repository 内使用生成字段与 Query，实现核心接口。
6. 添加 Repository 回归测试和必要的 MySQL 集成测试。

所有查询必须使用 `WithContext(ctx)`。普通过滤、排序、分页和更新禁止使用字符串列名；特殊锁、数据库表达式或复杂 SQL 只能封装在 Repository 内，并注释说明生成 API 无法表达的原因。Casbin 策略表由其 Adapter 管理，不纳入生成。

## 模块生成规范

新增业务模块先定义 `schemas/<module>.yaml`，再执行 `campusctl module validate` 和 `campusctl generate module`。v1 仅用于单实体兼容，新增模块使用 v2，并声明联合索引、主实体、外键依赖和 `operations`。HTTP operation 是模块 API 的单一事实来源；模块 OpenAPI、Casbin 权限、全局 HTTP 适配器及最终 `api/openapi.yaml` 均由它派生，禁止重复手写路由或权限配置，也禁止绕过 OpenAPI 校验或 Casbin 入口鉴权。

首次生成后，日常修改执行 `make generate`，提交前执行 `make generate-check` 和 `make migration-check`。允许重新生成 `.gen.go`、`api/modules/*.yaml`、`permissions/modules/*.json`、HTTP adapter 和全局 OpenAPI；禁止手工修改生成文件或覆盖 `domain/rule.go` 等人工扩展点。业务 Handler、领域规则和复杂事务仍须手写。普通生成不得创建正式迁移，迁移只能通过 lifecycle 命令创建草案并 promote。

所有认证写 operation 必须在 Schema 显式声明 `idempotency: required|inherent|none`。`required` operation 使用最长 128 字符的 `Idempotency-Key`，并由生成 adapter 传递 operation、actor、key 与规范化请求摘要；禁止在 Handler 重复解析生成参数。

## Go 风格与测试

代码必须通过 `gofmt`，包名使用简短小写单词，导出标识符使用 `PascalCase`，文件名使用小写蛇形。优先编写表驱动测试，覆盖成功、边界、权限失败、乐观锁和基础设施错误。生成代码不得直接作为 HTTP 响应；API DTO 由 OpenAPI 生成并由 Handler 显式映射。

## 安全与提交

禁止提交 `.env`、真实密钥或生产地址。提交建议遵循 Conventional Commits，例如 `feat(db): add activity repository`。PR 必须说明迁移、生成文件、配置影响和实际执行的验证命令。

创建或更新 PR 后，必须由 PR CI 执行完整的 `make generate-check`、`make migration-check`、`make test-race`、`make test-core-coverage`、`make test-generator`、`make vet`、`make lint`、`make build`、`make check-architecture`、`git diff --check` 和 `make test-compose`。必须持续检查当前 head commit 对应的全部 CI jobs，确认每个 job 均以 `success` 结束。若任一 job 失败、超时或被取消，必须查看日志定位原因，完成修复并推送后重新检查；不得以“本地未运行完整测试”为由忽略 CI 结果，在全部 jobs 成功前禁止合并 PR。

请求链路顺序为 Request ID → Body/Header 限制 → 认证 → 权限 → OpenAPI 校验 → Typed Params → 幂等控制 → 业务事务 → Domain Event/Outbox。production 必须启用 Redis TLS；运维证书文件读取必须保持明确路径边界和审计说明。
