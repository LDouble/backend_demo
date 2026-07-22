# 校园应用平台后端

平台提供用户认证、Redis 会话、Casbin RBAC、配置中心、数据库迁移和健康检查，并已加入通知中心、Schema v2 多实体生成及 Asynq 异步投递。

交易能力拆分为 Trade、Marketplace 和 Payment 三个边界：统一订单保存交易事实，Marketplace 仅保存商品与保留，Payment 预留在线支付、退款和回调模型。架构说明见 [`docs/trade-payment-architecture.md`](docs/trade-payment-architecture.md)。

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
go run ./cmd/worker
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

## API 与模块代码生成

平台基础接口及共享组件维护在 `api/openapi.base.yaml`；业务模块在 `schemas/<module>.yaml` 的 `operations` 中声明 HTTP 方法、路径、请求参数、响应和入口权限。生成器据此产生模块 OpenAPI、权限清单和 HTTP 适配器，并将基础契约与模块操作确定性汇总到公开契约 `api/openapi.yaml`。禁止直接修改聚合后的公开契约。

日常修改 Schema 后统一执行：

```bash
make generate
```

生成顺序固定为：全部模块产物 → 全局 OpenAPI → oapi-codegen/GORM Query。生成文件包含 DTO、ServerInterface、参数解析、Gin 路由注册和模块适配器，禁止手工修改。请求结构由 OpenAPI 校验中间件统一校验；认证、Casbin 鉴权、请求 ID、日志和异常恢复同样集中在中间件中。

`master` 分支的公开契约变化通过 `.github/workflows/sync-admin-openapi.yml` 同步到私有管理端仓库。后端仓库需要配置 `ADMIN_REPO_TOKEN` Actions Secret；该 fine-grained PAT 只授权 `LDouble/campus-admin-web`，并授予 Contents 与 Pull requests 写权限。工作流会更新固定分支 `chore/sync-backend-openapi` 上的同一个同步 PR，管理端生产构建只消费已合并的契约快照。

## 数据库代码生成

核心实体保存在 `internal/core/model`，GORM Gen 只生成 `internal/infrastructure/mysql/query` 中的类型安全 Query。修改 Model 或新增表后，在生成器中登记实体并执行：

```bash
make generate
make check-architecture
```

MySQL Repository 必须使用生成字段执行普通 CRUD，并通过 `WithContext(ctx)` 传递上下文；核心层和 API 层不得引用生成包。完整流程见 [`docs/database-development.md`](docs/database-development.md)。版本化 SQL 迁移仍是表结构的唯一事实来源。

## 业务模块生成

模块以版本化 YAML Schema 为输入。v1 兼容单实体，v2 支持多实体、联合索引和表间依赖。活动示例可用于校验流程，通知模块则可直接重新生成：

```bash
go run ./cmd/campusctl module validate schemas/examples/activity.yaml
rm -rf /tmp/campus-generator-example
go run ./cmd/campusctl generate module schemas/examples/activity.yaml --root /tmp/campus-generator-example
go run ./cmd/campusctl generate module schemas/examples/activity.yaml --check --root /tmp/campus-generator-example
make generate-module SCHEMA=schemas/activity.yaml
go run ./cmd/campusctl module validate schemas/notice.yaml
make generate-module SCHEMA=schemas/notice.yaml
go run ./cmd/campusctl module list
```

首次生成会创建 Domain、Application、GORM Query Repository、Handler、OpenAPI 片段、HTTP 适配器、迁移草案和权限清单，并将所有模块实体登记到共享 GORM Gen。后续只需维护 Schema 和手写业务 Handler，再执行 `make generate`；路由、契约、入口权限和适配签名无需重复配置。`.gen.go` 可重复生成；`domain/rule.go` 与测试骨架只在不存在时创建。模块迁移必须审核并编号后才能进入主迁移目录，生成器不会自动执行或编号迁移。完整流程见 [`docs/module-development.md`](docs/module-development.md)。

## 通知中心

Compose 会同时启动 API 和长期运行的 `worker`。管理员通知接口位于 `/api/v1/admin/notices`，个人收件箱位于 `/api/v1/notices`。发布时由 Worker 快照收件人；`push` 首版由日志 Provider 模拟，日志只包含通知 ID、用户 ID 和状态，不记录正文。Worker 使用 Redis DB 1，可通过 `CAMPUS_WORKER_REDIS_DB`、`CAMPUS_WORKER_CONCURRENCY` 和 `CAMPUS_WORKER_POLL_INTERVAL` 覆盖默认值。

## Staging 模板

通用单机 staging 模板位于 [`deploy/staging`](deploy/staging/README.md)。模板仅由 Caddy 暴露 80/443，内部服务使用固定私有网络，Redis 强制 TLS 与客户端证书；镜像、域名和全部凭据必须由受控环境文件或挂载 Secret 注入。
