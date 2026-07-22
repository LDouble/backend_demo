#!/bin/sh

set -eu

for package in \
  ./internal/modules/notice/domain \
  ./internal/modules/notice/application
do
  profile=$(mktemp)
  trap 'rm -f "$profile"' EXIT HUP INT TERM
  go test -coverprofile="$profile" "$package"
  coverage=$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%", "", $3); print $3}')
  awk -v coverage="$coverage" -v package="$package" 'BEGIN { if (coverage < 80) { printf "%s 覆盖率 %.1f%% 低于 80%%\n", package, coverage; exit 1 } }'
  rm -f "$profile"
  trap - EXIT HUP INT TERM
done
