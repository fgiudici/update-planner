/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plcc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// APIURL is the Red Hat Product Life Cycle API endpoint.
const APIURL = "https://access.redhat.com/product-life-cycles/api/v2/products"

// MajorMinorRegex matches version strings in MAJOR.MINOR format (e.g. "4.12").
var MajorMinorRegex = regexp.MustCompile(`^\d+\.\d+$`)

// Catalog holds the product lifecycle data returned by the PLCC API.
type Catalog struct {
	Data []Product `json:"data"`
}

// Product represents a software product with its lifecycle versions.
type Product struct {
	Name     string    `json:"name"`
	Package  string    `json:"package"`
	Versions []Version `json:"versions"`
}

// Version represents a product version with its lifecycle phases and platform compatibility.
type Version struct {
	Name                   string  `json:"name"`
	Phases                 []Phase `json:"phases"`
	OpenShiftCompatibility string  `json:"openshift_compatibility"`
}

// Phase represents a lifecycle phase with start and end dates (ISO8601 timestamps).
type Phase struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// ValidationResult records the outcome of validating a package or version.
type ValidationResult struct {
	PackageName string   `json:"packageName"`
	Version     string   `json:"version,omitempty"`
	Valid       bool     `json:"valid"`
	Reasons     []string `json:"reasons,omitempty"`
}

// Fetch retrieves the product catalog from the PLCC API.
func Fetch() (*Catalog, error) {
	resp, err := http.Get(APIURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var catalog Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &catalog, nil
}

// Load reads the product catalog from a local JSON file.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading PLCC file: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decoding PLCC file: %w", err)
	}
	return &catalog, nil
}

// FilterPackages removes products that have no package name, modifying the catalog in place.
func (c *Catalog) FilterPackages() {
	filtered := c.Data[:0]
	for _, p := range c.Data {
		if p.Package != "" {
			filtered = append(filtered, p)
		}
	}
	c.Data = filtered
}

// SortByPackage sorts products by package name in ascending order.
func (c *Catalog) SortByPackage() {
	sort.Slice(c.Data, func(i, j int) bool {
		return c.Data[i].Package < c.Data[j].Package
	})
}

// Dump writes the catalog products to a JSON file.
func (c *Catalog) Dump(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(c.Data)
}

// ValidateVersion checks that a version has a valid MAJOR.MINOR name, valid OCP compatibility
// values, and continuous phases with parseable timestamps. Returns a list of validation reasons
// (empty if valid).
func ValidateVersion(v Version) []string {
	var reasons []string

	if !MajorMinorRegex.MatchString(v.Name) {
		reasons = append(reasons, fmt.Sprintf("version name %q is not MAJOR.MINOR", v.Name))
	}

	if v.OpenShiftCompatibility != "" && v.OpenShiftCompatibility != "N/A" {
		for _, p := range strings.Split(v.OpenShiftCompatibility, ",") {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" && !MajorMinorRegex.MatchString(trimmed) {
				reasons = append(reasons, fmt.Sprintf("OCP compatibility %q is not MAJOR.MINOR", trimmed))
			}
		}
	}

	filtered, filterReasons := FilterPhases(v.Phases)
	reasons = append(reasons, filterReasons...)

	if len(filtered) == 0 && len(filterReasons) == 0 {
		reasons = append(reasons, "no phases after filtering")
		return reasons
	}

	type parsedPhase struct {
		name       string
		start, end time.Time
	}
	var parsed []parsedPhase
	for _, ph := range filtered {
		start, errS := ParseTimestamp(ph.StartDate)
		end, errE := ParseTimestamp(ph.EndDate)
		if errS != nil {
			reasons = append(reasons, fmt.Sprintf("phase %q start_date: %v", ph.Name, errS))
		}
		if errE != nil {
			reasons = append(reasons, fmt.Sprintf("phase %q end_date: %v", ph.Name, errE))
		}
		if errS != nil || errE != nil {
			continue
		}
		if !end.After(start) {
			reasons = append(reasons, fmt.Sprintf("phase %q: end (%s) is not after start (%s)", ph.Name, FormatDate(end), FormatDate(start)))
			continue
		}
		parsed = append(parsed, parsedPhase{name: ph.Name, start: start, end: end})
	}

	for i := 1; i < len(parsed); i++ {
		expectedStart := parsed[i-1].end.AddDate(0, 0, 1)
		if !parsed[i].start.Equal(expectedStart) {
			reasons = append(reasons, fmt.Sprintf("phase %q start (%s) must be one day after previous phase %q end (%s)",
				parsed[i].name, FormatDate(parsed[i].start), parsed[i-1].name, FormatDate(parsed[i-1].end)))
		}
	}

	return reasons
}

// FilterPhases removes non-applicable phases (both dates unset) and validates point-in-time
// phases. Returns the filtered normal phases and any validation reasons.
func FilterPhases(phases []Phase) ([]Phase, []string) {
	var reasons []string

	type indexedPhase struct {
		index int
		phase Phase
	}
	var normal, pointInTime []indexedPhase
	for i, ph := range phases {
		startUnset := IsUnset(ph.StartDate)
		endUnset := IsUnset(ph.EndDate)
		switch {
		case startUnset && endUnset:
			// Discard silently
		case !startUnset && !endUnset:
			normal = append(normal, indexedPhase{i, ph})
		default:
			pointInTime = append(pointInTime, indexedPhase{i, ph})
		}
	}

	if len(normal) == 0 {
		if len(pointInTime) > 0 {
			reasons = append(reasons, "no normal phases (with both start and end set)")
		} else {
			reasons = append(reasons, "no phases after filtering")
		}
		return nil, reasons
	}

	firstNormal := normal[0]
	lastNormal := normal[len(normal)-1]

	for _, pt := range pointInTime {
		ph := pt.phase
		if IsUnset(ph.StartDate) {
			if pt.index < firstNormal.index && ph.EndDate == firstNormal.phase.StartDate {
				continue
			}
		} else {
			if pt.index > lastNormal.index && ph.StartDate == lastNormal.phase.EndDate {
				continue
			}
		}
		if IsUnset(ph.StartDate) {
			reasons = append(reasons, fmt.Sprintf("phase %q is point-in-time (start unset, end %s) not aligned with first normal phase start (%s) or not at the beginning",
				ph.Name, ph.EndDate, firstNormal.phase.StartDate))
		} else {
			reasons = append(reasons, fmt.Sprintf("phase %q is point-in-time (start %s, end unset) not aligned with last normal phase end (%s) or not at the end",
				ph.Name, ph.StartDate, lastNormal.phase.EndDate))
		}
	}

	filtered := make([]Phase, len(normal))
	for i, n := range normal {
		filtered[i] = n.phase
	}
	return filtered, reasons
}

// IsUnset reports whether a date string is empty or "N/A".
func IsUnset(s string) bool {
	return s == "" || s == "N/A"
}

// ParseTimestamp parses an ISO8601 timestamp as used by the PLCC API (e.g. "2007-06-01T00:00:00.000Z").
func ParseTimestamp(s string) (time.Time, error) {
	if s == "N/A" || s == "" {
		return time.Time{}, fmt.Errorf("timestamp is %q (unset)", s)
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid ISO8601 timestamp %q: %w", s, err)
	}
	return t, nil
}

// FormatDate formats a time value as "YYYY-MM-DD".
func FormatDate(t time.Time) string {
	return t.Format("2006-01-02")
}
