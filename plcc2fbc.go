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

	"github.com/fgiudici/update-planner/fbc"
	"github.com/fgiudici/update-planner/plcc"
	"sigs.k8s.io/yaml"
)

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
	pipeline := fbc.DefaultFilters()

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

		pkg := fbc.NewPackage(entry.product)
		reasons := pkg.Filter(pipeline...)
		if len(reasons) > 0 {
			logEnc.Encode(plcc.ValidationResult{
				PackageName: pkgName,
				Valid:       false,
				Reasons:     reasons,
			})
			continue
		}

		yamlBytes, err := yaml.Marshal(pkg)
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
