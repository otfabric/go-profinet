#!/bin/sh
# Fetches third-party copies of the GSDML schemas V1.0, V2.0, V2.1, V2.2
# and V2.35 at pinned commits into .xsd/ (git-ignored: the schemas are
# PI's, the copies are not ours to redistribute, and they are never
# committed here), checks their SHA-256, and writes the DataType audit of
# gengsdml -audit to docs/research/gsdml-xsd-audit.json.
# Usage: scripts/gsdml-xsd-audit.sh [-check]
#   -check: fail when the committed report differs from the regenerated one.
# Sources (provenance only; see docs/research/GSDML-XSD-AUDIT.md):
#   V1.0-V2.2  github.com/DaltonManning/lastCBOpenIF, no licence stated
#   V2.35      github.com/TINF17C/GSD2AML-Converter, AGPL-3.0 repository
set -eu
cd "$(dirname "$0")/.."
DIR=.xsd
DM=https://raw.githubusercontent.com/DaltonManning/lastCBOpenIF/b8251053e28a2951f154f60c4cb0ca991cac1614/ControlBuilderMProfessional/Parsers/Profinet/XSD
TI=https://raw.githubusercontent.com/TINF17C/GSD2AML-Converter/d1a35b7c0d0db2385634cfb6befc208ebd1e960c/src/Gsd2Aml.Lib/Models/xsd

# version base file sha256
files() {
	cat <<'LIST'
1.0 DM GSDML-DeviceProfile-V1.0.xsd 393fa942830766ae39cb1b06bb473ba071ac756f128642cf9b4c61cac1d22773
1.0 DM GSDML-Primitives-v1.0.xsd 7793272171de6292d09616b109ba969209824009165c63b5651324c296b5f625
2.0 DM GSDML-DeviceProfile-V2.0.xsd 2e034df6b4a759efd4a2d908c0bb4bff5ae9c253df7bb6a896bcf743b6a806ee
2.0 DM GSDML-Primitives-v2.0.xsd f1e871f98716798f5b254160c71addcde0e07d01711db51799be130a2a7e311f
2.1 DM GSDML-DeviceProfile-V2.1.xsd 2bddea7ddbbc909191d8dfb90506e300cd7a7011096c2f460744bb4d640fd54d
2.1 DM GSDML-Primitives-v2.1.xsd 79e5ab82887d5b53747f5a82c617bb348a4968a8b972b1258e602ebda6df1fc8
2.2 DM GSDML-DeviceProfile-v2.2.xsd bc3fdc77f7ad1738b45cb40fbb3a1806d056b9f3162e98084b3c987a196bc12e
2.2 DM GSDML-Primitives-v2.2.xsd 7ec3312fe8a1e776bb2570701439309482579dc756d9265fe196a4f98f70b7dd
2.35 TI GSDML-DeviceProfile-v2.35.xsd 02568f3846cb616128115f760af803925c597809b0c9f5fb349c562840fb822b
2.35 TI GSDML-Primitives-v2.35.xsd 4a6b6258c6156edede5c4c456d714e5959f53e8a87cfabdfe5b6a08c10b0033e
LIST
}

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

files | while read -r v base f sum; do
	mkdir -p "$DIR/v$v"
	out="$DIR/v$v/$f"
	if [ ! -f "$out" ]; then
		case $base in DM) url=$DM/$f ;; *) url=$TI/$f ;; esac
		curl -fsSL --proto =https -o "$out.tmp" "$url"
		mv "$out.tmp" "$out"
	fi
	if [ "$(sha "$out")" != "$sum" ]; then
		echo "gsdml-xsd-audit: $out: SHA-256 mismatch" >&2
		exit 1
	fi
done

roots=""
for spec in 1.0:GSDML-DeviceProfile-V1.0.xsd 2.0:GSDML-DeviceProfile-V2.0.xsd 2.1:GSDML-DeviceProfile-V2.1.xsd 2.2:GSDML-DeviceProfile-v2.2.xsd 2.35:GSDML-DeviceProfile-v2.35.xsd; do
	v=${spec%%:*}
	roots="$roots${roots:+,}$v=$DIR/v$v/${spec#*:}"
done
report=docs/research/gsdml-xsd-audit.json
if [ "${1:-}" = -check ]; then
	go run ./internal/cmd/gengsdml -audit "$roots" | cmp - "$report"
else
	go run ./internal/cmd/gengsdml -audit "$roots" >"$report"
fi
