#!/bin/sh
# Fetches and verifies the GSDML reference schemas, kept in the private
# repository otfabric/profinet-gsdml-schema because their licence does not
# allow redistributing them (docs/research/GSDML.md). go-profinet never
# commits them; it uses a checkout for validation and code generation only.
#
#   scripts/gsdml-schemas.sh fetch    clone the repository at REF into DIR
#                                     (needs read access to it)
#   scripts/gsdml-schemas.sh verify   check DIR: at REF when it is a git
#                                     checkout, every file matching its
#                                     SHA256SUMS, well-formed, and every
#                                     DeviceProfile compiling with xmllint,
#                                     without network
#   scripts/gsdml-schemas.sh dir      print the xsd/ directory, exit 1 when
#                                     the schemas are not fetched
#   scripts/gsdml-schemas.sh differential
#                                     the differential test of
#                                     gsdml.Validate against xmllint
#                                     (gsdml/xsd_differential_test.go);
#                                     GSDML_CORPUS adds a directory
#
# GSDML_SCHEMAS is DIR, an existing checkout (default .gsdml-schemas);
# GSDML_SCHEMAS_REF the commit to use (default below).
set -eu
cd "$(dirname "$0")/.."
REPO=https://github.com/otfabric/profinet-gsdml-schema
REF=${GSDML_SCHEMAS_REF:-4b3051a6c538f610a009c231861926b3ea2236a3}
DIR=${GSDML_SCHEMAS:-.gsdml-schemas}

case ${1:-} in
fetch)
	if [ -d "$DIR/.git" ]; then
		git -C "$DIR" fetch -q origin
	else
		git clone -q "$REPO" "$DIR"
	fi
	git -C "$DIR" -c advice.detachedHead=false checkout -q "$REF"
	echo "gsdml-schemas: $DIR at $REF"
	;;
dir)
	[ -f "$DIR/xsd/SHA256SUMS" ] || exit 1
	echo "$DIR/xsd"
	;;
differential)
	[ -f "$DIR/xsd/SHA256SUMS" ] || { echo "gsdml-schemas: no schemas in $DIR: run scripts/gsdml-schemas.sh fetch or set GSDML_SCHEMAS" >&2; exit 1; }
	GSDML_SCHEMAS_XSD=$(cd "$DIR/xsd" && pwd) go test -count=1 -tags gsdmlxsd -run TestXSDDifferential ./gsdml
	;;
verify)
	[ -f "$DIR/xsd/SHA256SUMS" ] || { echo "gsdml-schemas: no schemas in $DIR: run scripts/gsdml-schemas.sh fetch or set GSDML_SCHEMAS" >&2; exit 1; }
	if [ -d "$DIR/.git" ]; then
		head=$(git -C "$DIR" rev-parse HEAD)
		[ "$head" = "$REF" ] || { echo "gsdml-schemas: $DIR is at $head, not $REF" >&2; exit 1; }
		[ -z "$(git -C "$DIR" status --porcelain)" ] || { echo "gsdml-schemas: $DIR has local changes" >&2; exit 1; }
	fi
	sh "$DIR/scripts/verify.sh" "$DIR/xsd"
	;;
*)
	echo "usage: scripts/gsdml-schemas.sh fetch | verify | dir | differential" >&2
	exit 2
	;;
esac
