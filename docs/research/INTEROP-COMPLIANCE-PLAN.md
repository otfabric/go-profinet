# PROFINET interoperability, compliance and hardening plan

Status: **wave A done**; waves B–E are proposals. Work packages A1–A7
shipped in go-profinet v0.12.0 (§14); the line references in §4 point at
the code as it was before them. Baseline of the investigation: go-profinet v0.11.0 (dcb01dc),
profinet-interop v0.9.0 (go-profinet pins v0.8.0), profinetctl (pins
go-profinet v0.10.0). Date 2026-11-21.

Evidence labels used throughout: **VERIFIED** (reproduced here, by a test
or a probe), **OBSERVED-IN-IMPLEMENTATION** (seen in another stack's code
or capture, named), **INFERRED** (reasoned from code, not run),
**UNVERIFIED** (claimed or plausible, no evidence yet). Nothing in this
document is a conformance claim; docs/FEATURE-STATUS.md stays the record of
what is claimed.

The seed corpus manifest is [gsdml-corpus/candidates.jsonl](gsdml-corpus/candidates.jsonl)
(§6). It holds URLs and metadata only, no vendor files.

## 1. Executive summary

- go-profinet is in good shape. `make check` is green with 97.5% coverage,
  39 fuzz targets run green, and the interop matrix has 248 pass,
  6 unsupported and 14 known-difference cells against p-net, pnio-rs and
  profinet-py.
- The largest weakness is **GSDML input breadth**. Every GSDML test file
  was written by us or generated at runtime. No third-party vendor file has
  ever been run through the pipeline in CI. Issue #74 (a vendor file
  without BitLength) showed what that costs.
- An audit of `gsdml/model.go` found one **confirmed defect** that
  silently writes wrong parameter bytes. Integer DefaultValues are parsed
  with Go's base-0 rules, so `"010"` becomes 8 (D1, §4). Several smaller
  silent-acceptance problems (D2–D5) sit next to it.
- Corpus milestone 1 is met **at the "identified" stage only**:
  449 distinct artifact URLs, at most 193 distinct contents. Nothing has
  been acquired, hashed (SHA-256), validated or tested, and no file is
  confirmed legally redistributable. Milestones 2–8 have not started; their
  actual numbers are in §6.
- The main structural gap in interop is that there is **no independent
  IO-Controller in CI**. Every controller-side claim is tested against
  devices only.
- Proposed order:
  1. Fix D1–D3 (small, independent PRs).
  2. Build a fetch-on-demand corpus tool in profinet-interop, with no
     vendor files committed.
  3. Run the pipeline over the corpus, producing a feature/failure matrix.
  4. Use that matrix to rank the next GSDML work.
  5. Separately, refresh the interop pins and stale docs.

## 2. Architecture assessment

| Layer | Packages | Assessment |
|---|---|---|
| L2 transport | `ethernet`, `l2`, `l2/afpacket`, `l2/bpf`, `l2/npcap`, `vnet` | Clean split; vnet makes e2e and chaos deterministic. Raw backends are measured only by `make l2-test`. |
| Discovery | `dcp`, `lldp` | Mature; DCP Set serialisation fixed in v0.10.x (#69). LLDP ifIndex/OID deviation still open (§9). |
| RPC / CM | `rpc`, `pnio`, `controller`, `device` | State machines are single-owner, context-driven, with `Close`. ModuleDiff and timing were aligned with p-net in v0.11.0. |
| Cyclic / alarms | `rt`, `alarm` | Bounded queues. Watchdog/DHT verified in e2e and chaos. |
| Redundancy | `mrp` | Kernel-MRP weekly job. No independent MRP peer in the matrix. |
| Engineering data | `gsdml`, `model`, generated types | Catalog → generator → typed model is sound and checkable against a licensed XSD. The conversion layer (`ToModel`, `applyRef`) is where defects concentrate (§4). |

The architecture does not need restructuring for this initiative. The work
is input hardening, evidence and tooling.

## 3. Capability and test baseline

- **Gates:**
  - `make check`: generate-check, coverpkg, release-notes, fmt, staticcheck,
    golangci-lint, vet on four OSes, govulncheck, race tests, coverage
    ≥ 90 overall and ≥ 85 per package, examples, docsync.
  - Coverage at v0.11.0: 97.5% (VERIFIED in this session).
- **Fuzz:** 39 targets, 30 s each locally and 5 min nightly (extended.yml).
  This is VERIFIED; docs/FEATURE-STATUS.md still says 34 (§12).
- **e2e and chaos:** `./e2e` on vnet. Chaos runs 3 seeds per PR and 20
  nightly.
- **Soak:** soak.yml, 25 runs. It has not been run for v0.11.0.
- **Interop:** profinet-interop contract 1.3, `interop-result-v1`.
  - Adapters: p-net (GPL, local image only), pnio-rs (MIT/Apache),
    profinet-py (GPL, local only), and own/go-profinet (self-interop).
  - Matrix: 248 pass / 6 unsupported / 14 known-difference. The waivers
    are in `tests/known-differences.tsv`.
  - The v0.11.0 run gave 34 pass; the MRP and otfabric-image tests were
    skipped.
- **profinetctl:**
  - Commands: `gsdml inspect/validate/check/gsdx/modules/submodules/generate`,
    `ar`, and `connect` with events state/alarm/module-diff/summary.
  - Typed errors and exit codes.
  - Its GSDML tests use only documents generated at runtime.
- **GSDML evidence:** every fixture under `gsdml/testdata/fixtures` was
  written by OT Fabric. No third-party GSDML is tested anywhere in the
  three repositories (VERIFIED by search).

## 4. Confirmed defects and suspected weaknesses

Each item has a class, the location, the evidence, and a proposed work
package in §14.

### D1 — integer DefaultValue parsed with Go base-0 rules (confirmed defect, P0)

- **Problem:** `applyRef` parses integer defaults with
  `strconv.ParseUint/ParseInt(dv, 0, …)`:
  - [gsdml/model.go:539](../../gsdml/model.go) (bit path);
  - [:582](../../gsdml/model.go) (signed);
  - [:592](../../gsdml/model.go) (unsigned).
- **What base 0 accepts:**
  - `"010"` → 8 (octal);
  - `"-010"` → −8;
  - `"1_0"` → 10;
  - `"0b101"` → 5;
  - `"0o7"` → 7.
- **Why it is wrong:** xsd:integer lexical space is decimal and allows
  leading zeros, so `"010"` is ten. The record default sent to the device
  is therefore wrong, and no error is raised.
- **Evidence:** VERIFIED by a probe test in this session; the probe was
  removed afterwards.
- **Relevance:** it writes wrong parameter bytes in a PrmBegin/Write
  sequence. That is silent misconfiguration of a field device.
- **Proposed change:** one shared parser that accepts:
  - optional sign, then decimal digits (leading zeros are decimal);
  - `0x`/`0X` hexadecimal.

  It refuses `0b`, `0o`, `_` and every other form. Hexadecimal is
  already accepted by `ParseUint16/32` and by the fixtures' Const Data;
  whether vendor files use it in DefaultValue is UNVERIFIED until the
  corpus run (§7). Before merging, confirm hexadecimal is permitted for
  DefaultValue specifically, or document that it is a tolerance (§20 Q1).
- **Risk:** a vendor file that relies on octal would change meaning. That
  is not plausible, because GSDML has no octal notation.
- **Tests:** a table over the lexical forms above for every integer
  DataType and the bit path; a ToModel test on a fixture edit; extend the
  `FuzzApplyRef` corpus.
- **Acceptance:** `"010"` → 10 for every integer type; `0b`/`0o`/`_` are
  refused with `ErrMalformed`; existing fixtures produce byte-identical
  records.
- **Size:** S. **Repo:** go-profinet.

### D2 — string and octet defaults silently truncated or zero-padded (suspected defect, P1)

- **Where:** `copy(dst, dv)` and `copy(dst, b)` at
  [gsdml/model.go:574](../../gsdml/model.go) and
  [:580](../../gsdml/model.go).
- **Problem:**
  - A DefaultValue longer than Length is cut short without an error.
  - A shorter one is zero-padded. This padding is INFERRED; whether
    padding is correct for VisibleString needs the spec (§20 Q2).
- **Proposed change:** refuse an over-long value. Keep the padding only
  if it is backed by evidence.
- **Size:** S.
- **Status:** A2. Over-long values are refused; the padding is kept as a
  documented policy, since OPC 30144 v1.00 Table 58 states no padding rule
  (UNVERIFIED in the GSDML specification).

### D3 — invalid Boolean lexical values fall back to the default in ToModel (suspected defect, P1)

- **Problem:** `ParseBool(x, def)` returns the default for anything other
  than true/false/1/0:
  - [gsdml/model.go:204](../../gsdml/model.go);
  - [:231](../../gsdml/model.go), among others.
- **Mitigation already in place:** `Validate` reports the value, because
  the Boolean enum is checked there (VERIFIED).
- **Effect:** ToModel callers that skip Validate get a silently wrong
  capability.
- **Proposed change:** ToModel returns `ErrMalformed` for a value that is
  present and invalid.
- **Size:** S.

### D4 — malformed optional numeric attributes silently ignored (suspected defect, P2)

- **Attributes:** MinDeviceInterval, ObjectUUIDLocalIndex,
  HardwareRelease, NumberOfAR, the ReductionRatio lists, AllowedInSlots,
  SoftwareRelease.
- **Where:** [gsdml/model.go:185](../../gsdml/model.go),
  [:193–197](../../gsdml/model.go), [:233](../../gsdml/model.go),
  [:294](../../gsdml/model.go).
- **Problem:** all use `if err == nil`. A bad MinDeviceInterval leaves 0
  in the model, and the controller then derives its own timing.
- **Proposed change:** treat "absent" and "present but malformed"
  separately. Absent keeps today's behaviour; malformed returns an error.
  This depends on the absent/empty distinction (open card
  task_16110bd2).
- **Size:** M.

### D5 — DataType coverage gaps in `applyRef` (compliance gap, P2)

- **Problems:**
  - A Ref with DataType Boolean goes to the unsigned path, so `"true"` is
    refused.
  - `Integer16_S`/`Integer32_S` (the scaled types) cannot be negative.
  - Types wider than 8 octets get a zero-filled high part.
- **Evidence:** INFERRED from code. The DataType semantics need the GSDML
  spec table (§20 Q3).
- **Size:** M.
- **Status:** A6. Boolean is a one-octet codec (IEC 61158-6-10 §4.1.2);
  every DataType without a verified DefaultValue encoding is refused with
  ErrUnsupported (`_S` status bit positions and composite lexical forms
  are not in OPC 30144 v1.00 Tables 17/58). The catalog's DataType enum
  does not match the per-context V2.35 enums: a separate audit work
  package, independent of the corpus.

### D6 — record structure checks missing (robustness, P3)

- **Problems:**
  - Overlapping areas: `ParameterRecord.Check` reports two Consts that
    share octets (an error, D.4.7.3) and two Refs that share bits (a
    warning), and `Encode` refuses values for two overlapping fields. A
    Ref over a Const is allowed, the Ref taking precedence (ISO 15745-4
    Amd 1, D.4.7.4, as the maintainer read it). **Done in v0.18.0:**
    `gsdml.Check` of a document runs the record rules on every record,
    with `parameter-record` for a record ToModel refuses and the
    TransferSequence rule.
  - AllowedValues and ValueItem are not used by ToModel, so a default
    outside AllowedValues is accepted. `SubmoduleParameters` reports
    both, `ParameterRecord.Set` refuses a value outside AllowedValues, and
    `ParameterRecord.Check` warns about a default outside them.
  - A BitArea cannot span octets. That matches Table D.31 as we read it
    (VERIFIED against the issue #74 research); re-check it against the
    V2.4x XSD.
- **Size:** M.

### D7 — ToModel is all-or-nothing (API improvement, P2)

- **Problems:**
  - One malformed module in UseableModules fails the whole DAP.
  - Only one DAP is converted per call.
- **Proposed change:** an opt-in per-module error report, or `ToModels`
  for every DAP. This is a candidate for the corpus run (§7), which needs
  per-DAP results anyway.
- **Size:** M.
- **Done in v0.19.0:** `gsdml.AnalyzeModels` reports every DAP with
  ToModel's outcome, error and model (converted only), and every module
  reference with its own conversion and slots; outcomes `converted`,
  `malformed`, `unsupported`, `resource_exhausted`, `blocked`. ToModel is
  unchanged. The corpus run (C1/C2/C5, profinet-interop) is still to do.

### D8 — XML hardening relies on encoding/xml defaults (robustness, P3)

- **Problem:** there is no explicit DTD refusal and no depth limit.
  encoding/xml does not expand external entities, so XXE is not an issue
  (INFERRED).
- **Proposed change:** refuse `<!DOCTYPE`, and add a depth and token
  budget with a fuzz target.
- **Size:** S.

### Smaller items

- **N1 (not a defect):** NaN and INF are accepted for Float. They are
  valid in xsd:float; document it.
- **N2:** absent and empty attributes are indistinguishable. This is the
  open card task_16110bd2 and the prerequisite of D4.

**Out of scope:** issues #72, #73 and #74 were closed in v0.11.0, and no
residual defect has been demonstrated.

## 5. Reusable research

- docs/research/GSDML.md §5 lists the seed repositories. Every one exists.
  None is archived or a fork (VERIFIED via the GitHub API).
- p-net removed its sample GSDML from the default branch. It is still
  present at tag v0.2.0 (commit 3e650720850e).
- apache/plc4x's `gsdml.xml` is that same p-net sample (per GSDML.md).
- pnio-rs's `sample-16real-32bool.xml` is identical at both commits
  (git blob c5dad867…).
- docs/research/STACKS.md and INDEPENDENT-CONTROLLER.md describe the peer
  stacks and a manual CODESYS controller recipe.
- The EVIDENCE-*.md files hold the capture-based reasoning for
  AR/SR/MCR/UDP, DCP/PDEV and MRP/PTCP. Their open deviations are in §9.

## 6. GSDML corpus: acquisition and licensing strategy

**Rules:**
- No vendor file is committed.
- The manifest records the source, an immutable commit (for git sources),
  the URL, and the SHA-256 once a file is acquired.
- A fetch tool downloads into a local cache that is never published, and
  verifies the hashes.
- Sources behind an email form, a login or a membership are excluded.
- Repository sources are pinned to commits, never to branches.

**Funnel, actual numbers as of 2026-11-21:**

| Stage | Count | Notes |
|---|---|---|
| Discovered (source known, no artifact URL) | 24 | Siemens, Control Techniques (login), ifm, Janitza, WAGO, SMC (JavaScript-only), Balluff, Phoenix, Festo, Weidmüller, Danfoss (HTTP 403), Turck, SEW, SICK, P+F, Hilscher, PI (pointer only) |
| Identified (artifact URL known) | 449 distinct URLs | 388 GitHub paths pinned to commits in 50 repositories, plus 61 vendor URLs (Beckhoff 45, UR 4, FMS 5, ATI 3, Lenze 1, Moxa 1, HMS 2 behind a form) |
| Distinct contents among identified | ≤ 193 | 132 distinct git blobs plus 61 vendor archives. Archives may hold several files or overlap. |
| Acquired | **0** | Nothing downloaded yet; needs your approval (§20 Q5) |
| Valid (parses as GSDML) | 0 | — |
| Unique (by SHA-256 of content) | 0 | — |
| Legally usable for local testing | undetermined | Public vendor downloads, used unmodified and locally: a legal judgement for you (§20 Q6) |
| Legally redistributable | **0 confirmed** | No page states redistribution terms. A repository licence (MIT and so on) does not relicense a vendor's file copied into it. |
| Tested | 0 | — |

**Milestones:**

| # | Target | Status |
|---|---|---|
| 1 | 200 candidate URLs | Met: 449 |
| 2 | 30 legally usable unique files | Not met. 0 acquired. Reachable from Beckhoff plus UR, FMS, ATI and Lenze alone, if Q6 is answered yes. |
| 3 | 100 or more files | Not started. Plausible: 132 GitHub blobs plus the contents of the Beckhoff archives. |
| 4–8 | Pipeline, matrix, categorised failures, minimised cases, 500 files | Not started |

**GSDML version spread across the 132 distinct GitHub blobs** (taken from
filenames, UNVERIFIED until parsed):

| Versions | Blobs |
|---|---|
| 2.0–2.25 | 21 |
| 2.3–2.35 | 68 |
| 2.4–2.46 | 40 |
| No version in the filename | 3 |

No GSDX file was found on GitHub. Beckhoff publishes one GSDX V1.0
archive.

**Proposed tooling (profinet-interop, WP-C1):**
- `corpus/manifest.jsonl`, the curated subset of candidates.jsonl.
- `scripts/corpus-fetch` (Go, standard library). It fetches into
  `$XDG_CACHE_HOME/otfabric/gsdml-corpus`, records SHA-256, unpacks zip
  archives with zip-slip and size limits, and refreshes the manifest's
  `sha256` and `archive_member` fields.
- A `corpus` workflow that only runs by hand or on a schedule. General CI
  never depends on it.

**Manifest fields:** candidates.jsonl has every field the addendum asks
for. Fields not yet known are null:
- `sha256`, `archive_member`, `vendor_id`/`device_id` and
  `feature_inventory` stay null until a file is acquired.
- `stage` is discovered or identified.
- `git_blob_sha1` is not SHA-256.
- `duplicate_of` points to the first URL with the same blob.

## 7. GSDML semantic coverage matrix (planned)

The corpus run writes one row per (file, DAP). Columns:

- **Parse:** ParseBytes result.
- **Validate:** the error classes Validate reports.
- **ToModel:** result and error class.
- **Sizes:** module, submodule and record counts.
- **Record checks:** every Ref DataType hit; whether the record defaults
  round-trip.
- **Present:** IOCR timing, PROFIsafe, IRT, MRP, ChannelDiagnosis,
  Alarm types, Channel ProcessAlarmReason.
- **GSDX:** for GSDX archives only.
- **Round-trip:** the result of `gsdml generate`.

From that matrix we derive the "feature frequency × failure rate" ranking
that orders the GSDML work packages after D1–D3.

## 8. Protocol compliance gaps

| Gap | Class | Evidence | Note |
|---|---|---|---|
| D1, D2, D5 record defaults | compliance gap | VERIFIED / INFERRED | §4 |
| No IOCR timing in controller.ARInfo from the negotiated response; Type hard-coded IOCARSingle | API / compliance gap | INFERRED from code | The device side got this in v0.11.0; the controller side reports config values |
| MaxAlarmDataLength 256 vs p-net 200 | interop difference | OBSERVED-IN-IMPLEMENTATION (p-net) | waived in known-differences.tsv |
| USI 0x8002 vs 0x8000 for diagnosis alarms | interop difference | OBSERVED-IN-IMPLEMENTATION | needs the spec reading |
| AlarmCR block order, IOCS placement | interop difference | captures | §9 |

## 9. Unresolved capture deviations

These come from testdata/captures/README.md and known-differences.tsv. Each
needs a spec reading or a second independent capture before code changes.

- LLDP: ifIndex/OID encoding.
- AlarmCR block order in the Connect response.
- MaxAlarmDataLength 256 vs 200.
- IOCS placement in the output frame.
- USI 0x8002 vs 0x8000.
- ERR-RTA echo behaviour.
- The I&M0FilterData GE case.
- The `%n%n%n` station name: intentional, it is a robustness test.

## 10. Interop opportunities

1. **An independent IO-Controller:** the main gap.
   - Options: CODESYS Control (commercial, so manual evidence only, per
     INDEPENDENT-CONTROLLER.md); the p-net "pn_dev" sample is a device, not
     a controller.
   - Look for an open-source controller: check the open-source controller
     candidates in STACKS.md (§20 Q8).
2. **An MRP peer:** the kernel-MRP job exists, but there is no stack-level
   MRP peer.
3. **GSDML cross-check:** run peer tools over the same corpus.
   - Python GSDML parsers in python-profinet (GPL, oracle in a container
     only).
   - plc4x's GSDML reader (Apache-2.0; its existence as a standalone reader is
     UNVERIFIED).
4. **Pins:**
   - Bump INTEROP_REF to v0.9.0 in the Makefile, interop.yml and
     kernel-mrp.yml:32.
   - Bump profinet-interop's own/go-profinet and profinetctl to v0.11.0.

## 11. Security and runtime hardening

- D8: XML DTD and depth limits.
- The corpus fetch tool itself:
  - zip-slip and decompression-bomb limits;
  - files from the internet are untrusted: parse them in a separate
    process with a timeout.
- Add an allocation budget for `ParseBytes` on the 31.6 MB Siemens ET200SP
  file. This gives a real-world upper bound instead of a synthetic one.
- `TestCheckReductionRatiosBounded` is load-sensitive because it measures
  process-wide TotalAlloc. Measure per call with `testing.AllocsPerRun`
  instead. This is not a budget change.

## 12. API, CLI and docs opportunities

- **Device:** a `Stats()` counterpart to the controller's (API
  improvement, P3).
- **Controller:** timing in ARInfo from the negotiated IOCR (§8).
- **profinetctl:**
  - `gsdml check --json`, emitting the §7 row per DAP, so the corpus run
    and the CLI share one code path;
  - test it on corpus files fetched on demand.
- **Docs:**
  - the FEATURE-STATUS header is stale (v0.8.1, "34 fuzz targets");
  - COVERAGE.md was measured at v0.8.1;
  - document N1.

## 13. Repository responsibilities

| Repository | Owns |
|---|---|
| go-profinet | D1–D8 fixes, regression tests from minimised corpus cases (our own text, no vendor content), a ToModels/per-module report, docs |
| profinet-interop | Corpus manifest, fetch tool, cache, corpus workflow, feature/failure matrix, peer adapters, pin bumps |
| profinetctl | `gsdml check --json`, a corpus smoke test (opt-in), bump to v0.12.0 |

## 14. Prioritised work packages (proposed PRs)

Each line is one small PR that can be reviewed on its own.

Done, released in go-profinet v0.12.0 (RELEASE.md): A1 (#77), A2 (#84),
A3 (#79), A4 (#81), A5 (#82), A6 (#85), A7 (#83). With them: the release
checks of #85 added a FuzzParse seed per package and the Validate step to
their fixture tests (§16). Open from wave A: the DataType enumeration of
the catalog per context and schema version (§4 D5, LIMITATIONS.md), and
the `_S` encodings, which need the bit positions.

| WP | PR | Class | Pri | Size | Repo | Depends |
|---|---|---|---|---|---|---|
| A1 | Decimal/hexadecimal integer DefaultValue parser (D1) | confirmed defect | P0 | S | go-profinet | — |
| A2 | Refuse over-long string and octet defaults (D2) | suspected defect | P1 | S | go-profinet | Q2 |
| A3 | ToModel errors on an invalid Boolean that is present (D3) | suspected defect | P1 | S | go-profinet | — |
| A4 | Absent vs empty attributes (N2, task_16110bd2) | API/robustness | P1 | M | go-profinet | — |
| A5 | Malformed optional numeric attributes are errors (D4) | suspected defect | P2 | M | go-profinet | A4 |
| A6 | DataType coverage in applyRef (D5) | compliance gap | P2 | M | go-profinet | Q3 |
| A7 | DTD refusal and depth budget (D8) | robustness | P3 | S | go-profinet | — |
| B1 | Bump INTEROP_REF to v0.9.0 | docs/tooling | P1 | S | go-profinet | — |
| B2 | Refresh FEATURE-STATUS header and COVERAGE.md | docs/tooling | P2 | S | go-profinet | — |
| B3 | Bump go-profinet to v0.12.0 | docs/tooling | P1 | S | profinet-interop, profinetctl | — |
| C1 | Corpus manifest and fetch tool (no vendor files) | new feature | P1 | M | profinet-interop | Q5, Q6 |
| C2 | Corpus run producing the §7 matrix (manual/scheduled workflow) | new feature | P1 | M | profinet-interop | C1, D7 |
| C3 | `gsdml check --json` | API improvement | P2 | S | profinetctl | — |
| C4 | Per-DAP / per-module conversion report (D7); done in v0.19.0 (`gsdml.AnalyzeModels`) | API improvement | P2 | M | go-profinet | — |
| C5 | Minimised regression cases from corpus failures | test | P1 | per case | go-profinet | C2 |
| E1 | Controller ARInfo timing from the negotiated IOCR | API/compliance | P3 | M | go-profinet | — |
| E2 | Device Stats() | API improvement | P3 | S | go-profinet | — |
| E3 | Independent controller evidence | interop gap | P2 | L | profinet-interop | Q8 |

## 15. Dependencies

The dependencies are the "Depends" column of §14:

- A5 needs A4.
- C2 needs C1 and C4.
- C5 needs C2.
- A2, A6, C1 and E3 need answers in §20.

## 16. Test strategy and acceptance criteria

- **Every A-package:** a table test over valid, boundary and invalid
  forms; a fixture-edit test through ParseBytes → Validate → ToModel, as
  in `gsdml/bitlength_test.go`; and a seed added to the fuzz corpus for
  the affected decoder.
- **Byte-identical records:** existing fixtures must produce identical
  records unless the PR documents why not. Expected test values must not
  be changed to make the code pass.
- **Corpus (C2):** the workflow is deterministic over a fixed manifest and
  writes the matrix as JSON.
  - A file that fails yields a categorised failure: parse, validate,
    model, or record.
  - A regression case is minimised and **rewritten in our own words**
    before it goes into go-profinet (C5). The vendor's text is not copied.
- **Gates unchanged:** `make check`, plus `make fuzz` for codec changes
  and `make interop` for behaviour the peers see.

## 17. Backwards compatibility

- **A1:** changes the bytes produced for octal-looking or underscore
  defaults. This is a behaviour change, so the PR needs a `### Breaking`
  entry with a consumer assessment. Valid decimal and hexadecimal values
  are unaffected.
- **A2, A3, A5:** files that used to load may now be refused. Each PR
  needs a Breaking entry, and the corpus (C2) should run before the
  release to measure the impact.
- **A4:** changes the generated types and the catalog. gorelease will
  flag the API change.
- **C4:** additive.

## 18. CI/CD impact

- General CI is unchanged.
- The corpus workflow is opt-in (manual dispatch or weekly) in
  profinet-interop. It needs network access and never blocks a PR or a
  release.
- B1 moves the interop job to profinet-interop v0.9.0. Check the job for
  any contract change.

## 19. External evidence, licences and equipment

- **OPC 30144 v1.00 (public):** Table 58, "DataType attribute value of
  Ref element" (after GSDML Table 18), normative; Table 17, the status
  codes B/S/U/G. Used for A2 and A6. Neither gives `_S` bit positions or
  DefaultValue lexical forms.
- **V2.35 XSD, third-party copy (read only, not redistributed):** five
  per-context DataType enums; Ref DefaultValue is xsd:string.
- **GSDML spec and XSDs:** PI members only. The V2.35/V2.42 XSD defaults
  remain unverified (LIMITATIONS.md).
- **Vendor files:** no redistribution terms were found on any page
  checked. Treat them as all rights reserved; we keep the manifest, not
  the files.
- **Excluded access paths:** HMS needs an email form, and Control
  Techniques needs a login. These are excluded; we do not bypass them.
- **Hardware:** none is required for A–C.
  - E3 needs either a CODESYS licence (manual evidence) or an open-source
    controller.
  - A real Siemens controller would be the strongest evidence, but it is
    not available in CI.

## 20. Open questions for the maintainer

1. **Hexadecimal in DefaultValue:** is it permitted by the spec (can you
   check the members-only GSDML spec)? Or do we keep it as a documented
   tolerance?
2. **VisibleString defaults shorter than Length:** pad with zeros, pad
   with spaces, or refuse?
3. **Ref DataType semantics:** do you have the GSDML spec table for
   Boolean and the `_S` scaled types?
4. Should D1–D3 ship together as v0.12.0 with one Breaking section, or
   separately?
5. **Downloads:** may I download the open-access candidates into a local,
   unpublished cache for testing? These are Beckhoff, UR, FMS, ATI and
   Lenze, plus the GitHub raw URLs pinned to commits. That is about 500
   files, up to about 40 MB for the largest.
6. **Legal basis:** is local, unmodified use of publicly published vendor
   GSDML for interoperability testing acceptable to you? (Redistribution
   stays out either way.)
7. Should the corpus manifest live in profinet-interop, as proposed, or in
   go-profinet?
8. **Independent controller:** is a CODESYS licence available for manual
   runs, or should we look for an open-source controller?
