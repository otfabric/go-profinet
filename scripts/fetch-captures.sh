#!/usr/bin/env bash
# Fetches the raw captures that testdata/captures only references (their
# redistribution is not established, testdata/captures/README.md) into the
# git-ignored testdata/captures/.cache and checks their SHA256 against
# testdata/captures/manifest.json. make captures-extended runs it and then
# the tests that compare the committed excerpts with the raw files.
#
# Usage: scripts/fetch-captures.sh
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE="${ROOT}/testdata/captures/.cache"
ICS_REV=0ffbf023afb4e39373228b54851bb4a1f33a3ab8

# source  path in the source  SHA256 (as in manifest.json)
FILES="
ics-pcaps Profinet/pro.pcap f8a04ed840d4d1f57b14b6008eb99d135b3a5e21f41eab3746eb3383b435a1ab
ics-pcaps Profinet/pro3.pcap bda384b5cf3b7035f13ae3bdc860d5d971627c1ded3c5e588a7b8f559effb931
ics-pcaps Profinet/pro4.pcap 2a13ba6e2475fed2b03eccbec31edb239ff88f12c58f8dd5e9ff40a05fb47d45
ics-pcaps Profinet/pro5.pcap 55ceb6e5c1ca1490c24d51d23084a554cecb95bdd9e9ae82e04b97c237798bbb
ics-pcaps Profinet/pro6.pcap ebb4ac34bda04ce0f3acadb947ff9be14fe07b0bb17a3bbbfff17223288caea5
ics-pcaps LLDP/simens.pcap 94368f800671e76964dee9e2e45b7271289ff5b6c8c1f04a8ecbdbeae342237e
"

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

echo "${FILES}" | while read -r src path want; do
    [[ -z "${src}" ]] && continue
    dst="${CACHE}/${src}/${path}"
    mkdir -p "$(dirname "${dst}")"
    if [[ ! -f "${dst}" ]] || [[ "$(sha256 "${dst}")" != "${want}" ]]; then
        case "${src}" in
        ics-pcaps) url="https://raw.githubusercontent.com/EmreEkin/ICS-Pcaps/${ICS_REV}/${path}" ;;
        *) echo "unknown source ${src}" >&2; exit 1 ;;
        esac
        curl -fsSL --retry 3 -o "${dst}.part" "${url}"
        mv "${dst}.part" "${dst}"
    fi
    got="$(sha256 "${dst}")"
    if [[ "${got}" != "${want}" ]]; then
        echo "${src}/${path}: sha256 ${got}, manifest ${want}" >&2
        exit 1
    fi
    echo "ok ${src}/${path}"
done
