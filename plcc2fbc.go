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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	plccAPIURL   = "https://access.redhat.com/product-life-cycles/api/v2/products"
	plccPageSize = 500
	fbcSchema    = "io.openshift.packages.lifecycles.v1alpha1"
)

var majorMinorRegex = regexp.MustCompile(`^\d+\.\d+$`)

// PLCC API types

type PLCCResponse struct {
	Data []PLCCProduct `json:"data"`
}

type PLCCProduct struct {
	Name     string        `json:"name"`
	Package  string        `json:"package"`
	Versions []PLCCVersion `json:"versions"`
}

type PLCCVersion struct {
	Name                   string      `json:"name"`
	Phases                 []PLCCPhase `json:"phases"`
	OpenShiftCompatibility string      `json:"openshift_compatibility"`
}

type PLCCPhase struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// FBC output types

type FBCBlob struct {
	Schema   string       `json:"schema"`
	Package  string       `json:"package"`
	Versions []FBCVersion `json:"versions"`
}

type FBCVersion struct {
	Name                   string     `json:"name"`
	Phases                 []FBCPhase `json:"phases"`
	OpenShiftCompatibility []string   `json:"openshiftCompatibility,omitempty"`
}

type FBCPhase struct {
	Name      string `json:"name"`
	TimeBegin string `json:"timeBegin"`
	TimeEnd   string `json:"timeEnd"`
}

func main() {
	var outputPath string
	var plccDumpPath string

	flag.StringVar(&outputPath, "output", "", "path to write FBC YAML output (default: stdout)")
	flag.StringVar(&plccDumpPath, "plcc-dump", "", "path to write filtered PLCC entries (packages only) as JSON")
	flag.Parse()

	output := os.Stdout
	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			log.Fatalf("failed to create output file: %v", err)
		}
		defer f.Close()
		output = f
	}

	products, err := fetchPLCC()
	if err != nil {
		log.Fatalf("failed to fetch PLCC data: %v", err)
	}

	log.Printf("fetched %d products from PLCC", len(products))

	// Filter to products with a package name, sorted by package
	var withPackage []PLCCProduct
	for _, p := range products {
		if p.Package != "" {
			withPackage = append(withPackage, p)
		}
	}
	sort.Slice(withPackage, func(i, j int) bool {
		return withPackage[i].Package < withPackage[j].Package
	})

	if plccDumpPath != "" {
		if err := writePLCCDump(plccDumpPath, withPackage); err != nil {
			log.Fatalf("failed to write PLCC dump: %v", err)
		}
		log.Printf("wrote %d PLCC entries to %s", len(withPackage), plccDumpPath)
	}

	// Group by package name; detect duplicates
	type packageEntry struct {
		product    PLCCProduct
		ambiguous  bool
		otherNames []string
	}
	byPackage := make(map[string]*packageEntry)
	for _, p := range withPackage {
		if entry, ok := byPackage[p.Package]; ok {
			entry.ambiguous = true
			entry.otherNames = append(entry.otherNames, p.Name)
		} else {
			byPackage[p.Package] = &packageEntry{product: p}
		}
	}

	// Sort package names for deterministic output
	packageNames := make([]string, 0, len(byPackage))
	for name := range byPackage {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)

	log.Printf("found %d distinct packages", len(packageNames))

	// Validate all packages and versions, emitting structured JSON logs
	logEnc := json.NewEncoder(os.Stderr)
	blobCount := 0
	for _, pkgName := range packageNames {
		entry := byPackage[pkgName]

		if entry.ambiguous {
			logEnc.Encode(ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     []string{fmt.Sprintf("package appears in multiple products: %v", append([]string{entry.product.Name}, entry.otherNames...))},
			})
			continue
		}

		if len(entry.product.Versions) == 0 {
			logEnc.Encode(ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     []string{"package has no versions"},
			})
			continue
		}

		// Validate each version, collecting results
		packageValid := true
		for _, v := range entry.product.Versions {
			reasons := validateVersion(v)
			valid := len(reasons) == 0
			logEnc.Encode(ValidationResult{
				PackageName: pkgName,
				Version:     v.Name,
				Valid:       valid,
				Reasons:     reasons,
			})
			if !valid {
				packageValid = false
			}
		}

		if !packageValid {
			continue
		}

		blob, _ := buildFBCBlob(entry.product)
		yamlBytes, err := yaml.Marshal(blob)
		if err != nil {
			logEnc.Encode(ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     []string{fmt.Sprintf("failed to marshal YAML: %v", err)},
			})
			continue
		}

		if blobCount > 0 {
			fmt.Fprintln(output, "---")
		}
		fmt.Fprint(output, string(yamlBytes))
		blobCount++
	}

	log.Printf("wrote %d FBC blobs", blobCount)
}

func writePLCCDump(path string, products []PLCCProduct) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(products)
}

func fetchPLCC() ([]PLCCProduct, error) {
	var all []PLCCProduct
	page := 1
	for {
		url := fmt.Sprintf("%s?page_size=%d&page=%d", plccAPIURL, plccPageSize, page)
		resp, err := http.Get(url)
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

		var plccResp PLCCResponse
		if err := json.Unmarshal(body, &plccResp); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		if len(plccResp.Data) == 0 {
			break
		}
		all = append(all, plccResp.Data...)

		if len(plccResp.Data) < plccPageSize {
			break
		}
		page++
	}
	return all, nil
}

type ValidationResult struct {
	PackageName string   `json:"packageName"`
	Version     string   `json:"version,omitempty"`
	Valid       bool     `json:"valid"`
	Reasons     []string `json:"reasons,omitempty"`
}

func validateVersion(v PLCCVersion) []string {
	var reasons []string

	if !majorMinorRegex.MatchString(v.Name) {
		reasons = append(reasons, fmt.Sprintf("version name %q is not MAJOR.MINOR", v.Name))
	}

	// Validate OCP compatibility
	if v.OpenShiftCompatibility != "" && v.OpenShiftCompatibility != "N/A" {
		for _, p := range strings.Split(v.OpenShiftCompatibility, ",") {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" && !majorMinorRegex.MatchString(trimmed) {
				reasons = append(reasons, fmt.Sprintf("OCP compatibility %q is not MAJOR.MINOR", trimmed))
			}
		}
	}

	// Filter and validate phases
	filtered, filterReasons := filterPhases(v.Phases)
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
		start, errS := parseTimestamp(ph.StartDate)
		end, errE := parseTimestamp(ph.EndDate)
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
			reasons = append(reasons, fmt.Sprintf("phase %q: end (%s) is not after start (%s)", ph.Name, formatDate(end), formatDate(start)))
			continue
		}
		parsed = append(parsed, parsedPhase{name: ph.Name, start: start, end: end})
	}

	// Check continuity on successfully parsed phases
	for i := 1; i < len(parsed); i++ {
		expectedStart := parsed[i-1].end.AddDate(0, 0, 1)
		if !parsed[i].start.Equal(expectedStart) {
			reasons = append(reasons, fmt.Sprintf("phase %q start (%s) must be one day after previous phase %q end (%s)",
				parsed[i].name, formatDate(parsed[i].start), parsed[i-1].name, formatDate(parsed[i-1].end)))
		}
	}

	return reasons
}

// filterPhases removes discardable phases from the list:
// 1. Phases where both start and end are unset ("N/A"/empty) — not applicable, discard.
// 2. At most one point-in-time phase at the beginning, aligned with the first normal phase's start.
// 3. At most one point-in-time phase at the end, aligned with the last normal phase's end.
// 4. No other point-in-time phases are allowed.
// Returns the filtered list (normal phases only) and any validation reasons.
func filterPhases(phases []PLCCPhase) ([]PLCCPhase, []string) {
	var reasons []string

	// Separate into normal, point-in-time, and N/A-N/A phases with their original indices
	type indexedPhase struct {
		index int
		phase PLCCPhase
	}
	var normal, pointInTime []indexedPhase
	for i, ph := range phases {
		startUnset := isUnset(ph.StartDate)
		endUnset := isUnset(ph.EndDate)
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
		if isUnset(ph.StartDate) {
			// end is set — must be the one allowed point-in-time before the first normal phase
			if pt.index < firstNormal.index && ph.EndDate == firstNormal.phase.StartDate {
				continue // valid, discard
			}
		} else {
			// start is set — must be the one allowed point-in-time after the last normal phase
			if pt.index > lastNormal.index && ph.StartDate == lastNormal.phase.EndDate {
				continue // valid, discard
			}
		}
		// Invalid point-in-time
		if isUnset(ph.StartDate) {
			reasons = append(reasons, fmt.Sprintf("phase %q is point-in-time (start unset, end %s) not aligned with first normal phase start (%s) or not at the beginning",
				ph.Name, ph.EndDate, firstNormal.phase.StartDate))
		} else {
			reasons = append(reasons, fmt.Sprintf("phase %q is point-in-time (start %s, end unset) not aligned with last normal phase end (%s) or not at the end",
				ph.Name, ph.StartDate, lastNormal.phase.EndDate))
		}
	}

	filtered := make([]PLCCPhase, len(normal))
	for i, n := range normal {
		filtered[i] = n.phase
	}
	return filtered, reasons
}

func isUnset(s string) bool {
	return s == "" || s == "N/A"
}

func buildFBCBlob(product PLCCProduct) (*FBCBlob, error) {
	blob := &FBCBlob{
		Schema:  fbcSchema,
		Package: product.Package,
	}

	for _, v := range product.Versions {
		fbcVersion, err := convertVersion(v)
		if err != nil {
			return nil, fmt.Errorf("version %q: %w", v.Name, err)
		}
		blob.Versions = append(blob.Versions, *fbcVersion)
	}

	// Sort versions by semver ordering
	sort.Slice(blob.Versions, func(i, j int) bool {
		return compareMajorMinor(blob.Versions[i].Name, blob.Versions[j].Name) < 0
	})

	return blob, nil
}

func convertVersion(v PLCCVersion) (*FBCVersion, error) {
	if !majorMinorRegex.MatchString(v.Name) {
		return nil, fmt.Errorf("name %q is not MAJOR.MINOR", v.Name)
	}

	phases, err := convertPhases(v.Phases)
	if err != nil {
		return nil, err
	}

	fv := &FBCVersion{
		Name:   v.Name,
		Phases: phases,
	}

	if v.OpenShiftCompatibility != "" && v.OpenShiftCompatibility != "N/A" {
		parts := strings.Split(v.OpenShiftCompatibility, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" {
				continue
			}
			if !majorMinorRegex.MatchString(trimmed) {
				return nil, fmt.Errorf("OCP compatibility %q is not MAJOR.MINOR", trimmed)
			}
			fv.OpenShiftCompatibility = append(fv.OpenShiftCompatibility, trimmed)
		}
	}

	return fv, nil
}

func convertPhases(plccPhases []PLCCPhase) ([]FBCPhase, error) {
	filtered, reasons := filterPhases(plccPhases)
	if len(reasons) > 0 {
		return nil, fmt.Errorf("%s", reasons[0])
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no phases after filtering")
	}

	var fbcPhases []FBCPhase
	for _, ph := range filtered {
		start, err := parseTimestamp(ph.StartDate)
		if err != nil {
			return nil, fmt.Errorf("phase %q start_date: %w", ph.Name, err)
		}
		end, err := parseTimestamp(ph.EndDate)
		if err != nil {
			return nil, fmt.Errorf("phase %q end_date: %w", ph.Name, err)
		}
		if !end.After(start) {
			return nil, fmt.Errorf("phase %q: end (%s) is not after start (%s)", ph.Name, formatDate(end), formatDate(start))
		}

		fbcPhases = append(fbcPhases, FBCPhase{
			Name:      ph.Name,
			TimeBegin: formatDate(start),
			TimeEnd:   formatDate(end),
		})
	}

	// Check continuity: phase1.end must equal phase2.start - 1 day
	for i := 1; i < len(fbcPhases); i++ {
		prevEnd, _ := time.Parse("2006-01-02", fbcPhases[i-1].TimeEnd)
		currStart, _ := time.Parse("2006-01-02", fbcPhases[i].TimeBegin)
		expectedStart := prevEnd.AddDate(0, 0, 1)
		if !currStart.Equal(expectedStart) {
			return nil, fmt.Errorf("phase %q start (%s) must be one day after previous phase %q end (%s)",
				fbcPhases[i].Name, fbcPhases[i].TimeBegin, fbcPhases[i-1].Name, fbcPhases[i-1].TimeEnd)
		}
	}

	return fbcPhases, nil
}

func compareMajorMinor(a, b string) int {
	aParts := strings.SplitN(a, ".", 2)
	bParts := strings.SplitN(b, ".", 2)
	aMajor, _ := strconv.Atoi(aParts[0])
	bMajor, _ := strconv.Atoi(bParts[0])
	if aMajor != bMajor {
		return aMajor - bMajor
	}
	aMinor, _ := strconv.Atoi(aParts[1])
	bMinor, _ := strconv.Atoi(bParts[1])
	return aMinor - bMinor
}

func parseTimestamp(s string) (time.Time, error) {
	if s == "N/A" || s == "" {
		return time.Time{}, fmt.Errorf("timestamp is %q (unset)", s)
	}
	// Expected: "2007-06-01T00:00:00.000Z"
	t, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid ISO8601 timestamp %q: %w", s, err)
	}
	return t, nil
}

func formatDate(t time.Time) string {
	return t.Format("2006-01-02")
}
