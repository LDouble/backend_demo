# Campus Platform: Compose 随机凭据初始化

**Priority:** High
**Status:** Done
**Type:** Enhancement
**Created:** 2026-07-21
**Last Updated:** 2026-07-21

## Overview

将 MySQL、Redis、JWT、配置主密钥和初始管理员凭据从 `compose.yaml` 移出，降低固定开发凭据被复制到共享环境或随配置修改误提交的风险。

## User Story

**As a** 本地开发者
**I want** 首次启动时生成独立随机凭据
**So that** 无需手工编辑 Compose，也不会把真实秘密提交到仓库

## Implementation Overview

新增幂等安全的 `scripts/init-env.sh` 和 `make env`：使用系统加密随机源生成 `.env`，以 `umask 077` 限制文件权限，文件存在时拒绝覆盖。`compose.yaml` 仅使用 `${VAR:?提示}` 形式的必填变量，并为 Redis 启用密码认证。随机密码采用十六进制字符，确保可安全嵌入 MySQL DSN 和 Compose 环境变量。

## Features / Requirements

1. **随机初始化**
   - 生成 MySQL 用户/根密码、Redis 密码、JWT 密钥、AES-256 主密钥和管理员密码
   - 不覆盖已有 `.env`，避免凭据与持久化数据库失配

2. **安全注入**
   - `.env` 保持 Git 忽略，提交 `.env.example` 作为变量清单
   - Compose 不保留固定密码或密钥，缺少变量时快速失败
   - MySQL、Redis 健康检查使用注入后的凭据

3. **开发体验**
   - `make compose-up` 自动确保 `.env` 已初始化
   - README 说明初始化、查看管理员用户名及密码轮换注意事项

## Files to Create

- `.env.example`：非敏感变量清单
- `scripts/init-env.sh`：本地随机凭据生成器

## Files to Modify

- `compose.yaml`：改用环境插值并启用 Redis 密码
- `Makefile`：增加 `env` 初始化目标
- `README.md`：更新启动和凭据说明

## Libraries/Dependencies

- Docker Compose `.env` 插值与必填变量语法
- 系统 `openssl` 命令用于加密安全随机数生成

## Testing Requirements

- 首次执行生成权限受限且字段齐全的 `.env`
- 重复执行不会覆盖已有凭据
- 缺少 `.env` 时 `docker compose config` 明确失败
- 使用随机凭据可完成 Compose 启动、健康检查与管理员登录
- `git status` 不展示生成的 `.env`

---

**Implementation Notes:** `.env` 适用于本地 Compose；生产环境应使用部署平台的 Secret 管理能力。MySQL 数据卷创建后不能只修改 `.env` 完成密码轮换，必须同步修改数据库账号。
