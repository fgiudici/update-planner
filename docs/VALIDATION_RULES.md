# PLCC Data Validation Rules

This document describes the rules that `generate-lifecycle-fbc` uses to validate data from the Red Hat Product Life Cycle Checker (PLCC) API before generating FBC (File-Based Catalog) lifecycle blobs.

Validation is applied at three levels: **package**, **version**, and **phase**. A package is only emitted as an FBC blob if *all* of its versions and phases pass validation.

---

## Package-Level Rules

### 1. Package name must be present

Products without a `package` field are silently discarded during the initial filtering step.

### 2. Package must contain at least one version

If a product's `versions` array is empty, the package is rejected.

### 3. Package must map to exactly one product

If the same `package` value appears on multiple PLCC products, the package is marked **ambiguous** and rejected. The validation log lists all product names that share the package.

---

## Version-Level Rules

### 4. Version name must be `MAJOR.MINOR`

The version `name` must match the regex `^\d+\.\d+$` (e.g., `4.12`, `1.0`). Versions with patch components, pre-release suffixes, or any other format are rejected.

### 5. OpenShift compatibility values must each be `MAJOR.MINOR`

If the version has an `openshift_compatibility` field that is non-empty and not `"N/A"`:

- The field is split on commas.
- Each comma-separated value (after trimming whitespace) must independently match `^\d+\.\d+$`.
- Empty segments (from trailing commas, etc.) are ignored.

### 6. Version must have at least one valid normal phase after filtering

After phase filtering (see below), the version must retain at least one normal phase. If all phases are discarded or invalid, the version is rejected.

---

## Phase-Level Rules

Phases are first classified and filtered, then the surviving phases are validated for timestamp correctness and continuity.

### Phase Classification

Each phase in the PLCC data has a `start_date` and `end_date`. A date value is considered **unset** if it is empty (`""`) or `"N/A"`. Phases are classified into three categories:

| Category | start_date | end_date | Handling |
|---|---|---|---|
| **N/A phase** | unset | unset | Silently discarded |
| **Normal phase** | set | set | Kept for validation and output |
| **Point-in-time phase** | one set, one unset | | Subject to alignment rules (see below) |

### 7. Point-in-time phase alignment

Point-in-time phases are phases where exactly one of `start_date` or `end_date` is unset. They are allowed only in two specific positions:

- **Before the first normal phase**: A point-in-time phase with an unset `start_date` is valid only if:
  - Its original index in the phases array is before the first normal phase's index, AND
  - Its `end_date` exactly equals the first normal phase's `start_date`.

- **After the last normal phase**: A point-in-time phase with an unset `end_date` is valid only if:
  - Its original index in the phases array is after the last normal phase's index, AND
  - Its `start_date` exactly equals the last normal phase's `end_date`.

Any point-in-time phase that does not meet these criteria causes the version to be rejected. Valid point-in-time phases are discarded from the output (they are not included in the FBC blob).

### 8. If only point-in-time phases remain (no normal phases), the version is rejected

The error indicates "no normal phases (with both start and end set)".

### 9. Timestamps must be valid ISO 8601

Both `start_date` and `end_date` on normal phases must parse as ISO 8601 timestamps in the format:

```
2006-01-02T15:04:05.000Z
```

Values of `""` or `"N/A"` are treated as unset (caught earlier during filtering). Any other non-parseable value is rejected.

### 10. End date must be strictly after start date

For each normal phase, the parsed `end_date` must be strictly after (`>`) the parsed `start_date`. A phase where end equals start, or end is before start, is rejected.

### 11. Phases must be contiguous (no gaps or overlaps)

For consecutive normal phases, the start date of phase N must be exactly **one day after** the end date of phase N-1.

For example, if phase 1 ends on `2024-06-30`, phase 2 must start on `2024-07-01`. Any gap or overlap between adjacent phases causes the version to be rejected.

---

## Summary of rejection behavior

| Condition | Effect |
|---|---|
| Product has no `package` | Silently skipped |
| Package has no versions | Package rejected |
| Package maps to multiple products | Package rejected |
| Any version in the package fails validation | Entire package rejected |
| Version name not `MAJOR.MINOR` | Version rejected |
| OCP compatibility entry not `MAJOR.MINOR` | Version rejected |
| No normal phases after filtering | Version rejected |
| Invalid point-in-time phase position/alignment | Version rejected |
| Unparseable timestamp | Version rejected |
| Phase end not after start | Version rejected |
| Phase gap or overlap (not contiguous) | Version rejected |

All validation failures are logged as structured JSON to stderr with the `packageName`, `version` (if applicable), `valid: false`, and a `reasons` array describing each issue.
