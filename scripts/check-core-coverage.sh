#!/bin/sh

set -eu

threshold=80
packages=$(go list ./internal/core/...)

for package in $packages; do
	if [ "$package" = "github.com/weouc-plus/campus-platform/internal/core/model" ]; then
		continue
	fi
	output=$(go test -cover "$package")
	printf '%s\n' "$output"
	coverage=$(printf '%s\n' "$output" | sed -n 's/.*coverage: \([0-9][0-9.]*\)% of statements.*/\1/p')
	if [ -z "$coverage" ]; then
		printf '%s\n' "无法读取 $package 的覆盖率" >&2
		exit 1
	fi
	if ! awk -v actual="$coverage" -v required="$threshold" 'BEGIN { exit !(actual >= required) }'; then
		printf '%s\n' "$package 覆盖率 ${coverage}% 低于 ${threshold}%" >&2
		exit 1
	fi
done
