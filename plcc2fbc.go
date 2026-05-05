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
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fgiudici/update-planner/plcc"
	"sigs.k8s.io/yaml"
)

const (
	fbcSchema = "io.openshift.operators.lifecycles.v1alpha1"
)

// FBC output types

type FBCBlob struct {
	Schema   string       `json:"schema"`
	Package  string       `json:"package"`
	Versions []FBCVersion `json:"versions"`
}

type FBCVersion struct {
	Name                  string     `json:"name"`
	Phases                []FBCPhase `json:"phases"`
	PlatformCompatibility []Platform `json:"platformCompatibility,omitempty"`
}

type FBCPhase struct {
	Name      string `json:"name"`
	TimeBegin string `json:"timeBegin"`
	TimeEnd   string `json:"timeEnd"`
}

type Platform struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}

func main() {
	var outputPath string
	var plccDumpPath string
	var plccInputPath string

	flag.StringVar(&outputPath, "output", "", "path to write FBC YAML output (default: stdout)")
	flag.StringVar(&plccDumpPath, "plcc-dump", "", "path to write filtered PLCC entries (packages only) as JSON")
	flag.StringVar(&plccInputPath, "plcc-input", "", "path to read PLCC JSON input (default: fetch from API)")
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

	var catalog *plcc.Catalog
	var err error
	if plccInputPath != "" {
		catalog, err = plcc.Load(plccInputPath)
	} else {
		catalog, err = plcc.Fetch()
	}
	if err != nil {
		log.Fatalf("failed to load PLCC data: %v", err)
	}

	log.Printf("fetched %d products from PLCC", len(catalog.Data))

	catalog.FilterPackages()
	catalog.SortByPackage()

	if plccDumpPath != "" {
		if err := catalog.Dump(plccDumpPath); err != nil {
			log.Fatalf("failed to write PLCC dump: %v", err)
		}
		log.Printf("wrote %d PLCC entries to %s", len(catalog.Data), plccDumpPath)
	}

	blobCount := generateFBC(catalog.Data, output, os.Stderr)
	log.Printf("wrote %d FBC blobs", blobCount)
}

func generateFBC(products []plcc.Product, output io.Writer, logOutput io.Writer) int {
	type packageEntry struct {
		product    plcc.Product
		ambiguous  bool
		otherNames []string
	}
	byPackage := make(map[string]*packageEntry)
	for _, p := range products {
		if entry, ok := byPackage[p.Package]; ok {
			entry.ambiguous = true
			entry.otherNames = append(entry.otherNames, p.Name)
		} else {
			byPackage[p.Package] = &packageEntry{product: p}
		}
	}

	packageNames := make([]string, 0, len(byPackage))
	for name := range byPackage {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)

	logEnc := json.NewEncoder(logOutput)
	blobCount := 0
	for _, pkgName := range packageNames {
		entry := byPackage[pkgName]

		if entry.ambiguous {
			logEnc.Encode(plcc.ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     []string{fmt.Sprintf("package appears in multiple products: %v", append([]string{entry.product.Name}, entry.otherNames...))},
			})
			continue
		}

		if len(entry.product.Versions) == 0 {
			logEnc.Encode(plcc.ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     []string{"package has no versions"},
			})
			continue
		}

		packageValid := true
		for _, v := range entry.product.Versions {
			reasons := plcc.ValidateVersion(v)
			valid := len(reasons) == 0
			logEnc.Encode(plcc.ValidationResult{
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
			logEnc.Encode(plcc.ValidationResult{
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

	return blobCount
}

func buildFBCBlob(product plcc.Product) (*FBCBlob, error) {
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

	sort.Slice(blob.Versions, func(i, j int) bool {
		return compareMajorMinor(blob.Versions[i].Name, blob.Versions[j].Name) < 0
	})

	return blob, nil
}

func convertVersion(v plcc.Version) (*FBCVersion, error) {
	if !plcc.MajorMinorRegex.MatchString(v.Name) {
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
		var ocpVersions []string
		parts := strings.Split(v.OpenShiftCompatibility, ",")
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" {
				continue
			}
			if !plcc.MajorMinorRegex.MatchString(trimmed) {
				return nil, fmt.Errorf("OCP compatibility %q is not MAJOR.MINOR", trimmed)
			}
			ocpVersions = append(ocpVersions, trimmed)
		}
		if len(ocpVersions) > 0 {
			fv.PlatformCompatibility = []Platform{{
				Name:     "openshift",
				Versions: ocpVersions,
			}}
		}
	}

	return fv, nil
}

func convertPhases(plccPhases []plcc.Phase) ([]FBCPhase, error) {
	filtered, reasons := plcc.FilterPhases(plccPhases)
	if len(reasons) > 0 {
		return nil, fmt.Errorf("%s", reasons[0])
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no phases after filtering")
	}

	var fbcPhases []FBCPhase
	for _, ph := range filtered {
		start, err := plcc.ParseTimestamp(ph.StartDate)
		if err != nil {
			return nil, fmt.Errorf("phase %q start_date: %w", ph.Name, err)
		}
		end, err := plcc.ParseTimestamp(ph.EndDate)
		if err != nil {
			return nil, fmt.Errorf("phase %q end_date: %w", ph.Name, err)
		}
		if !end.After(start) {
			return nil, fmt.Errorf("phase %q: end (%s) is not after start (%s)", ph.Name, plcc.FormatDate(end), plcc.FormatDate(start))
		}

		fbcPhases = append(fbcPhases, FBCPhase{
			Name:      ph.Name,
			TimeBegin: plcc.FormatDate(start),
			TimeEnd:   plcc.FormatDate(end),
		})
	}

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
