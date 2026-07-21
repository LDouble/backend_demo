# 项目架构

## 依赖方向

```text
cmd/server → internal/app → HTTP/API
                             ↓
                       Application
                             ↓
                          Domain
                             ↑
                      Infrastructure
```

`internal/core` 提供认证、用户、权限和配置等跨模块能力；`internal/modules/<module>` 保存独立业务模块；`internal/infrastructure` 提供共享 MySQL、Redis、日志和迁移适配器。Domain 不得导入 API、GORM、Gin 或共享基础设施实现。

## 事实来源

- `schemas/`：业务模块结构和生成输入。
- `api/openapi.yaml`：平台公共 HTTP 契约；`api/modules/` 保存待合并的模块片段。
- `migrations/`：数据库历史事实；模块生成迁移位于 `migrations/modules/`，审核并编号后才能进入主迁移序列。
- `.agent/modules.json`：已生成模块索引。

## 生成边界

`.gen.go`、模块 OpenAPI、权限清单和模块迁移可由生成器覆盖。`domain/rule.go` 与 `domain/rule_test.go` 是手写扩展点，仅首次创建。生成器不自动注册路由、执行迁移或修改已编号迁移。
