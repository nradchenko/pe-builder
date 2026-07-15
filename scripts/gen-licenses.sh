#!/usr/bin/env bash
# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Generate THIRD-PARTY-LICENSES: the full license texts and NOTICE files of the
# transitive module closure that the pebuild and pewalk binaries link. The output
# is regenerated, never hand-edited — run it via `make licenses` after any change
# to the dependency set, and ship the result alongside released binaries.
set -euo pipefail

cd "$(dirname "$0")/.."

OUT=THIRD-PARTY-LICENSES
TPL=scripts/third-party-licenses.tpl
PKGS=(./cmd/pebuild ./cmd/pewalk)
SELF=github.com/nradchenko/pe-builder

# go-licenses builds the packages to discover the linked set; -buildvcs=false lets
# it run from a git worktree or an unclean checkout without a VCS stamp failure.
export GOFLAGS="${GOFLAGS:-} -buildvcs=false"

if ! command -v go-licenses >/dev/null 2>&1; then
	echo "go-licenses not found. Install it with:" >&2
	echo "  go install github.com/google/go-licenses@latest" >&2
	exit 1
fi

# Per-module license texts, rendered from the report template. Reporting both
# binaries together deduplicates modules shared by them; --ignore drops our own
# module (it is not a third party, and carries no module version to report).
go-licenses report "${PKGS[@]}" --ignore "$SELF" --template "$TPL" >"$OUT"

# Reproduce any NOTICE files shipped by linked modules (required by Apache-2.0 §4).
mods=$(go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' "${PKGS[@]}" \
	| sort -u | grep -v "^${SELF}\$")
notice_hdr=0
for m in $mods; do
	dir=$(go list -m -f '{{.Dir}}' "$m" 2>/dev/null) || continue
	[ -n "$dir" ] || continue
	for nf in "$dir"/NOTICE "$dir"/NOTICE.txt "$dir"/NOTICE.md; do
		[ -f "$nf" ] || continue
		if [ "$notice_hdr" -eq 0 ]; then
			{
				printf '\n\n'
				printf '%s\n' '================================================================================'
				printf 'NOTICE files (reproduced as required by Apache-2.0 §4)\n'
				printf '%s\n' '================================================================================'
			} >>"$OUT"
			notice_hdr=1
		fi
		{
			printf '\n--- %s ---\n\n' "$m"
			cat "$nf"
		} >>"$OUT"
	done
done

echo "wrote $OUT ($(grep -c '^License: ' "$OUT") modules)"