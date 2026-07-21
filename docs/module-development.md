# 业务模块开发与生成流程

## 单一事实来源

新增业务模块使用 `schemas/<module>.yaml` v2。Schema 同时声明持久化实体与 HTTP operations：

- `entities` 生成领域实体、Repository、迁移草案和 GORM Query 登记；
- `operations` 生成模块 OpenAPI、Casbin 权限清单和全局 HTTP adapter；
- 全部模块 OpenAPI 确定性汇总到 `api/openapi.yaml`，再由 oapi-codegen 生成 DTO、`ServerInterface` 和 Gin 路由。

不要在 Schema 之外重复维护同一个模块的路由或权限。业务 Handler、领域状态机、本人/交易双方权限和复杂事务仍是手写扩展点。

## 首次创建模块

```bash
go run ./cmd/campusctl module validate schemas/<module>.yaml
go run ./cmd/campusctl generate module schemas/<module>.yaml
make generate
```

首次生成会登记 `.agent/modules.json`，并创建模块目录、OpenAPI 片段、权限清单、HTTP adapter 和迁移草案。`domain/rule.go` 与测试骨架只在不存在时创建。

审核 `migrations/modules/<module>.up.sql` 和 `.down.sql` 后，为迁移分配连续版本号并复制到 `migrations/`。生成器不会执行迁移，也不会修改已编号迁移。

## 声明业务操作

每个 HTTP 能力只在 `operations` 中声明一次：

```yaml
operations:
  - operation_id: CreateMarketplaceOrder
    method: POST
    path: /api/v1/marketplace/orders
    permission: marketplace:order_create
    summary: 原子保留商品并创建订单
    headers:
      - {name: Idempotency-Key, type: string, required: true}
    body:
      fields:
        - {name: listing_id, type: integer, format: uint64, required: true, minimum: 1}
    responses:
      - {status: 201, kind: success}
      - {status: 409, kind: error}
```

生成器会校验重复 `operation_id`、重复 method/path、非法权限名、参数类型和响应状态。路径中的 `{id}` 自动生成必填 `uint64` path 参数；Header、query 和 JSON body 会进入 OpenAPI 请求校验。

生成的 adapter 将 `CreateMarketplaceOrder` 转发到同包的手写 `createMarketplaceOrder`。开发者只实现该业务方法，并调用 Application Manager；不得直接在 Handler 中实现数据库事务或资源所有权规则。

## 日常修改

修改任一已登记模块后执行：

```bash
make generate
```

该命令依次执行：

1. `campusctl generate modules`：刷新所有已登记模块产物；
2. `campusctl generate openapi`：移除旧模块 operation 并重新汇总全局契约；
3. `go generate ./...`：生成 oapi-codegen DTO/路由及 GORM Query。

不要手工修改以下文件：

- `api/modules/*.yaml`
- `permissions/modules/*.json`
- `internal/api/generated/api.gen.go`
- `internal/api/httpapi/*_adapter.gen.go`
- `internal/infrastructure/mysql/query/*.gen.go`
- 其他包含 `DO NOT EDIT` 的文件

## 提交前验证

```bash
make generate-check
make test-generator
make test-race
make vet
make lint
make build
make check-architecture
git diff --check
```

`make generate-check` 不接受生成漂移：它会检查所有模块、全局 OpenAPI、oapi-codegen 和 GORM Query。PR 需说明 Schema、迁移、权限、公开 API 和配置影响，并列出实际执行的验证命令。

## 模块准出测试（强制）

每个新增或修改业务模块在合并前必须具备并实际运行以下测试；仅有领域单元测试不能准出。

| 层级 | 最低覆盖内容 | 验证命令 |
| --- | --- | --- |
| Domain / Application | 输入边界、状态机、所有权、乐观锁和幂等规则 | `go test ./internal/modules/<module>/...` |
| HTTP | 至少一条成功闭环，以及未认证、入口权限或业务角色越权、OpenAPI 参数校验中的相关分支 | `go test ./internal/api/httpapi/...` 或 Compose API 测试 |
| MySQL 集成 | 编号迁移、关键事务原子性，以及存在竞争时的并发唯一成功或幂等重放 | `make test-compose` |
| 异步能力（如适用） | 领域事件、订阅去重、重试和最终可见结果 | `make test-compose` |

新模块的集成测试放在 `tests/integration/<module>_test.go`（多个紧密耦合模块可共用文件），并使用 `//go:build integration`。涉及金额、库存/名额、接单、支付或状态变更的模块必须有真实 MySQL 并发测试，不能以 SQLite 替代锁语义。所有模块变更的 PR 必须在说明中列出新增测试及实际运行的命令；缺少任一适用层级的测试视为未完成。
