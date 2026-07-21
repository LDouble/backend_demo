# Repository Guidelines

## 项目结构与模块组织

仓库目前处于方案设计阶段，核心文档为 `campus-platform-technical-design.md`，尚未提交可运行源码。实现时应遵循方案中的 Go 模块布局：入口放在 `cmd/server` 与 `cmd/campusctl`；基础能力放在 `internal/core`；业务按领域拆分到 `internal/modules/<module>`；数据库、Redis、日志和链路追踪适配器放在 `internal/infrastructure`。迁移、部署文件及 Agent 文档分别放入 `migrations/`、`deploy/` 和 `.agent/`。测试文件与被测代码同目录保存。

## 构建、测试与本地开发

当前仓库没有 `go.mod`、Makefile 或 Docker Compose 文件，暂不可构建。代码落地后应提供并保持以下标准命令可用：

```bash
go run ./cmd/server       # 启动本地 API 服务
go test ./...             # 运行全部单元测试
go test -race ./...       # 检查并发数据竞争
go vet ./...              # 执行静态检查
docker compose up -d      # 启动应用及 MySQL、Redis 等依赖
```

提交文档前至少运行 `git diff --check`，避免空白错误。

## 编码风格与命名约定

Go 代码必须通过 `gofmt`（建议执行 `gofmt -w .`）和 `go vet ./...`。包名使用简短小写单词；导出标识符使用 `PascalCase`，非导出标识符使用 `camelCase`；文件名采用小写蛇形命名，如 `signup_rule.go`。业务逻辑集中在 `domain`/`rule` 层，不直接修改生成器产出的代码。依赖方向保持 `API → Application → Domain`，基础设施层实现领域接口。

## 测试规范

使用 Go 标准 `testing` 包，文件命名为 `*_test.go`，测试函数命名为 `Test<对象>_<场景>`。优先使用表驱动测试覆盖正常路径、权限失败、边界值和基础设施错误；外部 MySQL、Redis 或微信接口应通过接口替身隔离。新增或修复业务规则必须附带回归测试。

## 提交与拉取请求

仓库尚无提交历史可供归纳。建议采用 Conventional Commits，例如 `feat(activity): add signup rule`、`docs: update architecture`。每次提交只包含一个逻辑变更。拉取请求应说明目的、实现范围、验证命令和配置/迁移影响；关联相关 Issue。涉及 API 或界面变化时，附请求示例、Swagger 差异或截图，并明确标注生成代码。

## 安全与配置

禁止提交真实密钥、微信凭据或生产数据库地址。启动参数写入本地 `bootstrap.yaml` 或环境变量，并提供脱敏示例；运行时 Secret 应加密存储。任何新接口都需同时考虑认证、Casbin 权限、数据隔离和审计日志。
