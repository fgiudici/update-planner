# FBC Lifecycle Schema: `io.openshift.operators.lifecycles.v1alpha1`

This document defines the File-Based Catalog (FBC) extension schema for operator lifecycle and compatibility metadata.

## Schema Overview

Each FBC lifecycle blob describes one operator package: its supported versions, the lifecycle tier and phases for each version, and the platforms each version is compatible with.

```yaml
schema: io.openshift.operators.lifecycles.v1alpha1
package: <string>
versions:
  - name: <string>
    phases:
      - name: <string>
        timeBegin: <string>
        timeEnd: <string>
    platformCompatibility:
      - name: <string>
        versions:
          - <string>
```

## Root Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `schema` | string | yes | Always `io.openshift.operators.lifecycles.v1alpha1`. Identifies this blob type within FBC. |
| `package` | string | yes | The OLM catalog package name (e.g., `aws-efs-csi-driver-operator`). Must match the operator's package in the catalog. |
| `versions` | array | yes | List of version entries. Each entry describes one minor release of the operator. |

## Version Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Version identifier in `MAJOR.MINOR` format (e.g., `4.12`, `1.5`). |
| `phases` | array | yes | Ordered list of lifecycle phases for this version. Must be contiguous (no gaps or overlaps). |
| `platformCompatibility` | array | no | Platforms this version is compatible with. Omitted if no compatibility data is available. |

## Phase Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Phase name (e.g., `Full support`, `Maintenance support`). |
| `timeBegin` | string | yes | Start date in `YYYY-MM-DD` format. |
| `timeEnd` | string | yes | End date in `YYYY-MM-DD` format. Must be strictly after `timeBegin`. |

### Phase Continuity

Phases within a version are ordered chronologically and must be contiguous: the start date of phase N must be exactly one day after the end date of phase N-1. There must be no gaps or overlaps between adjacent phases.

## Platform Compatibility Fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Platform identifier (e.g., `openshift`). |
| `versions` | array | yes | List of platform versions this operator version is compatible with, each in `MAJOR.MINOR` format. |

The `platformCompatibility` structure is designed to support multiple platforms. Currently, only `openshift` is populated from PLCC data.

## Example

```yaml
schema: io.openshift.operators.lifecycles.v1alpha1
package: aws-efs-csi-driver-operator
versions:
  - name: "4.12"
    phases:
      - name: Full support
        timeBegin: "2023-01-17"
        timeEnd: "2023-08-17"
      - name: Maintenance support
        timeBegin: "2023-08-18"
        timeEnd: "2024-07-17"
      - name: Extended update support
        timeBegin: "2024-07-18"
        timeEnd: "2025-01-17"
      - name: Extended update support Term 2
        timeBegin: "2025-01-18"
        timeEnd: "2026-01-17"
      - name: Extended update support Term 3
        timeBegin: "2026-01-18"
        timeEnd: "2027-01-17"
    platformCompatibility:
      - name: openshift
        versions:
          - "4.12"
  - name: "4.17"
    phases:
      - name: Full support
        timeBegin: "2024-10-01"
        timeEnd: "2025-05-25"
      - name: Maintenance support
        timeBegin: "2025-05-26"
        timeEnd: "2026-04-01"
    platformCompatibility:
      - name: openshift
        versions:
          - "4.17"
```

## Data Source

Lifecycle metadata originates from the Red Hat Product Life Cycle Center (PLCC) API. The `plcc2fbc` tool fetches, validates, and converts PLCC data into this schema. See [Validation Rules](VALIDATION_RULES.md) for entry-dropping criteria.

## Conversion Examples

- [PLCC input example](../schema-examples/plcc-schema-example.yaml) — a real PLCC entry (AWS EFS CSI driver operator)
- [FBC output example](../schema-examples/fbc-schema-example.yaml) — the same entry converted to FBC, with all unmapped fields annotated
