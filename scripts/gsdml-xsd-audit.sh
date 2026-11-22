#!/bin/sh
# Writes the two reports derived from the GSDML reference schemas (the
# private otfabric/profinet-gsdml-schema, fetched with
# scripts/gsdml-schemas.sh fetch at its pinned commit):
#
#   gsdml/schema/xsd-registry.json     gengsdml -registry: every GSDML
#                                      version, its files, checksums,
#                                      sizes, namespaces, revision dates
#                                      and imports
#   docs/research/gsdml-xsd-audit.json gengsdml -audit: the DataType
#                                      enumerations per context, for all
#                                      versions, oldest first
#
# Both hold facts about the schemas, never their text, so they are
# committed. Usage: scripts/gsdml-xsd-audit.sh [-check]
#   -check: fail when a committed report differs from the regenerated one.
set -eu
cd "$(dirname "$0")/.."
sh scripts/gsdml-schemas.sh verify >/dev/null
dir=$(sh scripts/gsdml-schemas.sh dir)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go run ./internal/cmd/gengsdml -registry "$dir" >"$tmp/registry"
go run ./internal/cmd/gengsdml -audit "$dir" >"$tmp/audit"
status=0
for pair in registry:gsdml/schema/xsd-registry.json audit:docs/research/gsdml-xsd-audit.json; do
	new=$tmp/${pair%%:*}
	report=${pair#*:}
	if [ "${1:-}" = -check ]; then
		cmp -s "$new" "$report" || { echo "gsdml-xsd-audit: $report is out of date; run scripts/gsdml-xsd-audit.sh" >&2; status=1; }
	else
		cp "$new" "$report"
	fi
done
exit $status
