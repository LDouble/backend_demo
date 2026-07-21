# AI 开发规则

## 新增模块

1. 先在 `.agent/TASKS` 与 `.agent/PRDS` 明确范围并完成评审。
2. 创建或修改 `schemas/<module>.yaml`。
3. 执行 `go run ./cmd/campusctl module validate <schema>`。
4. 在 `operations` 中声明方法、路径、参数、响应和权限；不得另行手写路由或权限清单。
5. 执行 `go run ./cmd/campusctl generate module <schema>` 完成首次登记。
6. 执行 `make generate`，再执行 `make generate-check` 校验模块、OpenAPI、HTTP adapter 和 GORM Query 漂移。
7. 审核模块 OpenAPI、权限清单及迁移，将迁移编号后再纳入主序列。
8. 只在 `rule.go`、扩展 Handler 和测试中补充业务逻辑。

## 禁止事项

- 不手工修改 `.gen.go` 或其他带 `DO NOT EDIT` 标记的文件。
- 不从 Domain 导入 Gin、GORM、Redis 或基础设施包。
- 不让生成器隐式执行迁移、连接生产数据库或覆盖手写扩展。
- 不在 SQL、配置或测试中提交真实密码和密钥。

## 数据库流程

数据库结构以版本化 SQL 为准。生成模块迁移先评审，再移动为成对的编号迁移；随后更新实体并运行 GORM Query 生成。手写 Repository 的普通查询必须使用生成字段和 `WithContext(ctx)`。

## 必跑检查

```bash
make generate-check
make test-generator
make test-race
make vet
make lint
make build
```
