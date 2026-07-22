#!/bin/sh

set -eu

target="${1:-.env}"

if ! command -v openssl >/dev/null 2>&1; then
	printf '%s\n' "缺少 openssl，无法生成安全随机凭据。" >&2
	exit 1
fi

if [ -e "$target" ]; then
	if grep -q '^CAMPUS_ACADEMIC_MATERIAL_KEY=' "$target"; then
		printf '%s\n' "$target 已存在，保留现有凭据。"
		exit 0
	fi
	umask 077
	academic_material_key=$(openssl rand -base64 32)
	printf '%s\n' "CAMPUS_ACADEMIC_MATERIAL_KEY=$academic_material_key" >>"$target"
	chmod 600 "$target"
	printf '%s\n' "$target 已存在；已保留原凭据并补充教务材料密钥。"
	exit 0
fi

directory=$(dirname "$target")
temporary=$(mktemp "$directory/.env.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM

mysql_password=$(openssl rand -hex 24)
mysql_root_password=$(openssl rand -hex 24)
redis_password=$(openssl rand -hex 24)
jwt_secret=$(openssl rand -hex 32)
config_master_key=$(openssl rand -base64 32)
academic_material_key=$(openssl rand -base64 32)
admin_password=$(openssl rand -hex 16)

umask 077
{
	printf '%s\n' "# 由 scripts/init-env.sh 自动生成，请勿提交或共享。"
	printf '%s\n' "CAMPUS_MYSQL_DATABASE=campus"
	printf '%s\n' "CAMPUS_MYSQL_USER=campus"
	printf '%s\n' "CAMPUS_MYSQL_PASSWORD=$mysql_password"
	printf '%s\n' "CAMPUS_MYSQL_ROOT_PASSWORD=$mysql_root_password"
	printf '%s\n' "CAMPUS_REDIS_PASSWORD=$redis_password"
	printf '%s\n' "CAMPUS_JWT_SECRET=$jwt_secret"
	printf '%s\n' "CAMPUS_CONFIG_MASTER_KEY=$config_master_key"
	printf '%s\n' "CAMPUS_ACADEMIC_MATERIAL_KEY=$academic_material_key"
	printf '%s\n' "CAMPUS_ADMIN_USERNAME=admin"
	printf '%s\n' "CAMPUS_ADMIN_PASSWORD=$admin_password"
	printf '%s\n' "CAMPUS_HTTP_PORT=8080"
	printf '%s\n' "CAMPUS_MYSQL_PORT=3306"
	printf '%s\n' "CAMPUS_REDIS_PORT=6379"
	printf '%s\n' "GOPROXY=https://goproxy.cn,direct"
} >"$temporary"
chmod 600 "$temporary"
mv "$temporary" "$target"
trap - EXIT HUP INT TERM

printf '%s\n' "已生成 ${target}（权限 600）。"
printf '%s\n' "管理员用户名为 admin；密码请在本地 ${target} 中查看。"
