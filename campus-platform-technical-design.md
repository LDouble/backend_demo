# 校园应用平台后端技术方案

版本：V2.0

## 1. 方案目标

本方案面向校园综合服务平台后端建设，覆盖教务服务、校园通知、活动报名、社区交流、二手交易、拼车、跑腿、失物招领等场景。

核心目标：

- AI First：降低 AI 理解成本，通过 Schema 和代码生成减少重复编码。
- 最大化复用成熟组件，避免重复造轮子。
- 配置后台化，减少硬编码。
- 模块化设计，支持快速扩展。
- 一键部署，降低安装成本。

---

## 2. 核心设计原则

### 2.1 Schema First

开发流程：

```
数据库 Schema
        ↓
代码生成器
        ↓
Model / DTO / Repository / Service / API / Swagger / Permission
```

业务开发：

```
定义模型
+
编写业务规则
+
生成代码
```

---

### 2.2 AI First

AI 开发原则：

- 不默认扫描整个项目。
- 优先读取 `.agent` 架构文档。
- 不修改生成代码。
- 业务逻辑集中在 rule/domain 层。

新增业务：

```
需求
 ↓
Schema
 ↓
Generator
 ↓
生成代码
 ↓
补充业务规则
 ↓
测试
```

---

# 3. 技术选型

|领域|技术|
|-|-|
|语言|Go|
|Web|Gin|
|ORM|GORM|
|数据库|MySQL 8|
|缓存|Redis|
|权限|Casbin|
|认证|JWT + Redis Session|
|任务|Asynq|
|日志|Zap|
|API文档|Swagger|
|指标|Prometheus|
|链路追踪|OpenTelemetry|
|部署|Docker Compose|

---

# 4. 总体架构

```
微信小程序
      |
 API Gateway
      |
 Go Application
      |
 -------------------------
 |          |             |
Core     Modules     Generator
 |
Infrastructure
 |
MySQL Redis OSS MQ
```

---

# 5. 项目结构

```
campus-platform

├── cmd
│   ├── server
│   └── campusctl

├── internal

│
├── core
│   ├── auth
│   ├── permission
│   ├── config
│   ├── audit
│   └── user

│
├── modules
│   ├── activity
│   ├── notice
│   ├── marketplace
│   ├── carpool
│   ├── lostfound
│   └── runner

│
├── infrastructure
│   ├── mysql
│   ├── redis
│   ├── logger
│   └── telemetry

│
├── generator

├── migrations

├── deploy

└── .agent
    ├── architecture.md
    ├── modules.json
    └── rules.md
```

---

# 6. 模块设计

业务模块独立：

```
activity

├── api
│   └── handler.go

├── application
│   └── service.go

├── domain
│   ├── entity.go
│   └── rule.go

├── infrastructure
│   └── repository.go

└── module.go
```

依赖：

```
API
 ↓
Application
 ↓
Domain

Infrastructure 实现接口
```

---

# 7. Core 基础能力

包含：

- 用户系统
- JWT认证
- Casbin权限
- 配置中心
- 审计日志
- 消息通知


---

# 8. 配置中心

原则：

> 能后台配置的不进入代码。


启动配置：

```
bootstrap.yaml
```

仅保存：

- 服务端口
- 数据库地址
- Redis地址


运行配置：

```
configs

id
group
key
value
encrypted
version
updated_by
```


示例：

```
wechat.appid

wechat.secret

activity.signup.enable

file.max_size

rate.login.limit
```

---

# 9. 权限系统

使用 Casbin RBAC。

支持：

- 用户权限
- 角色权限
- API权限
- 菜单权限
- 数据权限


权限自动生成：

```
@Permission(activity:create)

POST /activity
```

生成：

```
activity:create

/api/activity POST
```

---

# 10. 微信接入

微信配置数据库化：

```
wechat_configs

app_code
app_id
secret
template_config
status
```

后台维护：

```
管理后台
 ↓
系统配置
 ↓
微信设置
```

---

# 11. 代码生成系统

输入：

```yaml
entity:
  name: Activity

fields:
  - name: title
    type: string

  - name: start_time
    type: datetime
```

输出：

```
activity

model.go
dto.go
repository.go
service.go
handler.go
router.go
swagger.go
permission.go
```

---

# 12. 自动部署

Docker Compose：

包含：

- backend
- mysql
- redis
- nginx
- prometheus
- grafana
- loki


启动：

```bash
docker compose up -d
```

---

# 13. CLI工具

提供：

```
campusctl

init

migration

generate

module

config
```

生成模块：

```bash
campusctl generate module activity
```

---

# 14. 可观测性

日志：

Zap JSON：

```
trace_id
request_id
user_id
path
latency
error
```


指标：

```
http_request_total

http_latency

db_latency

redis_latency
```


链路：

OpenTelemetry覆盖：

- Gin
- GORM
- Redis
- HTTP Client


---

# 15. 安全设计

包含：

- HTTPS
- JWT密钥轮换
- Secret加密
- bcrypt密码
- API权限校验
- 数据权限隔离
- 文件上传限制
- 审计日志


---

# 16. 推荐开发顺序

第一阶段：

- Gin
- GORM
- Redis
- JWT
- Casbin
- 配置中心


第二阶段：

- Generator
- campusctl
- AI开发规范


第三阶段：

- 通知
- 活动
- 二手交易
- 拼车
- 跑腿


---

# 17. 最终目标

```
新增业务

↓

定义Schema

↓

生成80%代码

↓

AI补充20%规则

↓

上线
```

形成：

```
低代码 + AI生成 + 模块化校园平台
```
