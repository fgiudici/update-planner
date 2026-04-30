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
	"bytes"
	"io"
	"os"
	"testing"
)

func TestGoldenFile(t *testing.T) {
	products, err := loadPLCC("testdata/plcc.json")
	if err != nil {
		t.Fatalf("loading PLCC test data: %v", err)
	}

	var withPackage []PLCCProduct
	for _, p := range products {
		if p.Package != "" {
			withPackage = append(withPackage, p)
		}
	}

	var buf bytes.Buffer
	generateFBC(withPackage, &buf, io.Discard)

	want, err := os.ReadFile("testdata/golden-fbc.yaml")
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}

	if buf.String() != string(want) {
		t.Errorf("FBC output does not match golden file (got %d bytes, want %d bytes)", buf.Len(), len(want))
		os.WriteFile("testdata/actual-fbc.yaml", buf.Bytes(), 0644)
		t.Log("actual output written to testdata/actual-fbc.yaml")
	}
}
