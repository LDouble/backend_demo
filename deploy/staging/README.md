# Staging 单机部署手册

该模板只描述通用单机 staging，不包含真实主机、域名、证书或 Secret。仅 Caddy 映射宿主机 80/443；API、Worker、MySQL 与 Redis 位于固定私有网络，Redis 强制 TLS 和客户端证书认证。

## 准备与部署

1. 将 `.env.example` 复制到宿主机受控目录，填入不可变应用镜像摘要、域名和独立 staging 凭据；文件权限设为 `0600`，不要提交。
2. 从 `redis.conf.example` 创建受控的 Redis 配置 Secret，将 `requirepass` 改为与环境文件一致的随机值；生成独立 CA、服务端证书（SAN 含 `redis`）和客户端证书，宿主机私钥权限设为 `0600`。另创建权限为 `0600` 的教务 Mock 白名单 JSON，内容为 `{student_no, real_name, password_hash}` 数组且 `password_hash` 必须是 bcrypt。一次性 `redis-tls-init` 服务会把 Redis 文件复制到隔离卷，为 UID `65532` 的应用和 Redis 用户分别设置所有权，并把私钥权限收紧为 `0400`。
3. 在任何变更前校验配置并备份数据库：

   ```bash
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml config --quiet
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml exec -T mysql \
     sh -c 'exec mysqldump --single-transaction -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' \
     > "backup-$(date -u +%Y%m%dT%H%M%SZ).sql"
   ```

   同时以支持权限/所有权保留的备份工具快照 `academic-materials` 私有卷。数据库记录、材料卷和 `CAMPUS_ACADEMIC_MATERIAL_KEY` 必须作为同一恢复集保管；材料密钥不得与数据库备份放在同一访问域。

4. 拉取镜像并按依赖启动。`migrate` 成功后 `bootstrap` 才运行，二者成功后 API/Worker 才启动：

   ```bash
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml pull
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml up -d
   ```

   证书轮换后重新创建 `redis-tls-init`，再重启 Redis、API 和 Worker，使隔离卷中的副本同步更新。

5. 验证 HTTPS、就绪状态与 Redis mTLS：

   ```bash
   curl --fail --proto '=https' --tlsv1.2 https://staging.example.invalid/health/ready
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml ps
   docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml exec redis \
     sh -c 'REDISCLI_AUTH="$CAMPUS_REDIS_PASSWORD" exec redis-cli --tls \
     --cacert /run/secrets/redis-tls/ca.crt --cert /run/secrets/redis-tls/client.crt \
     --key /run/secrets/redis-tls/client.key ping'
   ```

## 恢复

先停止 API/Worker，确认备份文件与目标环境，再恢复；数据库容器和数据卷保留：

```bash
docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml stop api worker
docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml exec -T mysql \
  sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' < backup.sql
docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml start api worker
```

恢复材料时先保持 API/Worker 停止，以 UID `65532`、目录 `0750`、文件 `0600` 恢复私有卷，再恢复匹配的数据库和材料主密钥。禁止把材料目录交给 Caddy 或任何静态文件服务。

## 应用与数据库回滚

应用回滚只需把环境文件中的 `CAMPUS_IMAGE` 改为上一个已验证的镜像摘要，再执行 `pull` 和 `up -d api worker`。不要使用浮动 tag。

数据库默认不自动降级。只有在确认目标应用与迁移向后兼容、完成新备份并经人工批准后，才显式执行：

```bash
docker compose --env-file /secure/staging.env -f deploy/staging/compose.yaml run --rm migrate migration down 1
```

随后重新执行配置校验、就绪检查和核心交易冒烟流程。若迁移不可逆或兼容性不确定，使用备份恢复，不执行 down。
