#!/bin/sh

set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

cd "$root"
mkdir -p "$tmp/internal/api" "$tmp/internal/infrastructure/mysql"
cp -R internal/api/generated "$tmp/internal/api/generated"
cp -R internal/infrastructure/mysql/query "$tmp/internal/infrastructure/mysql/query"

go generate ./...

diff -ru "$tmp/internal/api/generated" internal/api/generated
diff -ru "$tmp/internal/infrastructure/mysql/query" internal/infrastructure/mysql/query
