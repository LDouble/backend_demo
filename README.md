# 校园应用平台后端

阶段 1 提供用户认证、Redis 会话、Casbin RBAC、配置中心、数据库迁移和健康检查。阶段 2 增加 Schema 驱动的模块生成器、`campusctl` 编排和 AI 开发规范。

## 本地启动

```bash
make env
make compose-up
curl http://localhost:8080/health/ready
```

若端口被占用，可通过 `CAMPUS_HTTP_PORT`、`CAMPUS_MYSQL_PORT`、`CAMPUS_REDIS_PORT` 覆盖宿主机端口。

`make env` 首次运行时使用 `openssl` 随机生成 MySQL、Redis、JWT、AES 主密钥和初始管理员凭据，并写入权限为 `600` 的 `.env`。该文件已被 Git 忽略；再次运行不会覆盖已有值。变量清单见 `.env.example`。

Docker 构建默认使用 `https://goproxy.cn,direct` 下载 Go 模块。CI 或其他网络环境可以通过 `GOPROXY` 覆盖。

管理员用户名默认为 `admin`，密码保存在本地 `.env`。登录前可以只导出管理员凭据：

登录示例：

```bash
export CAMPUS_ADMIN_USERNAME=$(sed -n 's/^CAMPUS_ADMIN_USERNAME=//p' .env)
export CAMPUS_ADMIN_PASSWORD=$(sed -n 's/^CAMPUS_ADMIN_PASSWORD=//p' .env)
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$CAMPUS_ADMIN_USERNAME\",\"password\":\"$CAMPUS_ADMIN_PASSWORD\"}"
```

不要通过删除 `.env` 直接轮换已有环境的 MySQL 密码：初始化后的数据库卷会保存原凭据。轮换时应先修改数据库账号，再同步更新 `.env`；生产部署应使用平台 Secret 管理能力。

## 手动运行

复制 `bootstrap.yaml.example` 为 `bootstrap.yaml`，设置以下必需环境变量：

- `CAMPUS_JWT_SECRET`：至少 32 字节
- `CAMPUS_CONFIG_MASTER_KEY`：Base64 编码的 32 字节 AES 密钥
- `CAMPUS_ADMIN_USERNAME` 与 `CAMPUS_ADMIN_PASSWORD`

然后运行：

```bash
go run ./cmd/campusctl migration up
go run ./cmd/campusctl admin bootstrap
go run ./cmd/server
```

## 验证

```bash
make test-race
make test-core-coverage
make test-generator
make vet
make lint
make build
make test-compose
```

`make test-compose` 使用隔离宿主机端口启动全新 Compose 环境，自动验证迁移升降级、真实 Redis 会话、Casbin 持久化、数据库密文、权限即时生效和 API 重启后持久化，结束后清理容器及数据卷。

## API 代码生成

`api/openapi.yaml` 是 HTTP 契约的唯一来源。修改契约后执行：

```bash
make generate
```

命令使用 Go 1.25 的 `tool` 依赖运行固定版本的 oapi-codegen，生成 `internal/api/generated/api.gen.go`。生成文件包含 DTO、ServerInterface、参数解析与 Gin 路由注册，禁止手工修改。请求结构由 OpenAPI 校验中间件统一校验；认证、Casbin 鉴权、请求 ID、日志和异常恢复同样集中在中间件中。

## 数据库代码生成

核心实体保存在 `internal/core/model`，GORM Gen 只生成 `internal/infrastructure/mysql/query` 中的类型安全 Query。修改 Model 或新增表后，在生成器中登记实体并执行：

```bash
make generate
make check-architecture
```

MySQL Repository 必须使用生成字段执行普通 CRUD，并通过 `WithContext(ctx)` 传递上下文；核心层和 API 层不得引用生成包。完整流程见 [`docs/database-development.md`](docs/database-development.md)。版本化 SQL 迁移仍是表结构的唯一事实来源。

## 业务模块生成

模块以版本化 YAML Schema 为输入。活动示例可用于校验流程，但不会自动写入业务目录：

```bash
go run ./cmd/campusctl module validate schemas/examples/activity.yaml
rm -rf /tmp/campus-generator-example
go run ./cmd/campusctl generate module schemas/examples/activity.yaml --root /tmp/campus-generator-example
go run ./cmd/campusctl generate module schemas/examples/activity.yaml --check --root /tmp/campus-generator-example
make generate-module SCHEMA=schemas/activity.yaml
go run ./cmd/campusctl module list
```

首次生成会创建 Domain、Application、GORM Query Repository、Handler、OpenAPI 片段、迁移草案和权限清单，并将模块实体登记到共享 GORM Gen。随后执行 `make generate-check` 生成类型安全 Query。`.gen.go` 可重复生成；`domain/rule.go` 与测试骨架只在不存在时创建。模块迁移必须审核并编号后才能进入主迁移目录，生成器不会自动执行迁移或注册路由。完整约束见 [`.agent/architecture.md`](.agent/architecture.md) 和 [`.agent/rules.md`](.agent/rules.md)。
