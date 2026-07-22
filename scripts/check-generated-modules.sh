#!/bin/sh
set -eu

go run ./cmd/campusctl module list | while IFS="$(printf '\t')" read -r module entity schema; do
  [ -n "$module" ] || continue
  [ -n "$entity" ] || { echo "module $module has no entity" >&2; exit 1; }
  [ -n "$schema" ] || { echo "module $module has no schema" >&2; exit 1; }
  go run ./cmd/campusctl generate module "$schema" --check
done
