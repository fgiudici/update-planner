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
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{"valid", "2025-11-11T00:00:00.000Z", time.Date(2025, 11, 11, 0, 0, 0, 0, time.UTC), false},
		{"N/A", "N/A", time.Time{}, true},
		{"empty", "", time.Time{}, true},
		{"malformed", "2025-13-01", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTimestamp(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	got := FormatDate(time.Date(2025, 3, 5, 14, 30, 0, 0, time.UTC))
	if got != "2025-03-05" {
		t.Errorf("got %q, want %q", got, "2025-03-05")
	}
}

func TestIsUnset(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"N/A", true},
		{"2025-01-01T00:00:00.000Z", false},
	}
	for _, tt := range tests {
		if got := IsUnset(tt.input); got != tt.want {
			t.Errorf("IsUnset(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFilterPackages(t *testing.T) {
	c := &Catalog{Data: []Product{
		{Name: "A", Package: "pkg-a"},
		{Name: "B", Package: ""},
		{Name: "C", Package: "pkg-c"},
	}}
	c.FilterPackages()
	if len(c.Data) != 2 {
		t.Fatalf("got %d products, want 2", len(c.Data))
	}
	if c.Data[0].Package != "pkg-a" || c.Data[1].Package != "pkg-c" {
		t.Errorf("unexpected packages: %q, %q", c.Data[0].Package, c.Data[1].Package)
	}
}

func TestSortByPackage(t *testing.T) {
	c := &Catalog{Data: []Product{
		{Package: "zebra"},
		{Package: "alpha"},
		{Package: "mid"},
	}}
	c.SortByPackage()
	want := []string{"alpha", "mid", "zebra"}
	for i, p := range c.Data {
		if p.Package != want[i] {
			t.Errorf("index %d: got %q, want %q", i, p.Package, want[i])
		}
	}
}

func TestFilterPhases(t *testing.T) {
	tests := []struct {
		name       string
		phases     []Phase
		wantCount  int
		wantErrors bool
	}{
		{
			name: "normal phases kept",
			phases: []Phase{
				{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-06-30T00:00:00.000Z"},
				{Name: "Maintenance", StartDate: "2025-07-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
			},
			wantCount:  2,
			wantErrors: false,
		},
		{
			name: "valid point-in-time at beginning discarded silently",
			phases: []Phase{
				{Name: "GA", StartDate: "N/A", EndDate: "2025-01-01T00:00:00.000Z"},
				{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
			},
			wantCount:  1,
			wantErrors: false,
		},
		{
			name: "misaligned point-in-time rejected",
			phases: []Phase{
				{Name: "GA", StartDate: "N/A", EndDate: "2025-03-01T00:00:00.000Z"},
				{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
			},
			wantCount:  1,
			wantErrors: true,
		},
		{
			name: "all unset phases",
			phases: []Phase{
				{Name: "EUS", StartDate: "N/A", EndDate: "N/A"},
			},
			wantCount:  0,
			wantErrors: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered, reasons := FilterPhases(tt.phases)
			if len(filtered) != tt.wantCount {
				t.Errorf("got %d filtered phases, want %d", len(filtered), tt.wantCount)
			}
			if (len(reasons) > 0) != tt.wantErrors {
				t.Errorf("reasons = %v, wantErrors = %v", reasons, tt.wantErrors)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		name      string
		version   Version
		wantValid bool
	}{
		{
			name: "valid version with continuous phases",
			version: Version{
				Name: "4.12",
				Phases: []Phase{
					{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-06-30T00:00:00.000Z"},
					{Name: "Maintenance", StartDate: "2025-07-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
				},
			},
			wantValid: true,
		},
		{
			name: "invalid version name",
			version: Version{
				Name: "4.12.1",
				Phases: []Phase{
					{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
				},
			},
			wantValid: false,
		},
		{
			name: "invalid OCP compatibility",
			version: Version{
				Name:                   "4.12",
				OpenShiftCompatibility: "4.12, latest",
				Phases: []Phase{
					{Name: "Full support", StartDate: "2025-01-01T00:00:00.000Z", EndDate: "2025-12-31T00:00:00.000Z"},
				},
			},
			wantValid: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reasons := ValidateVersion(tt.version)
			if (len(reasons) == 0) != tt.wantValid {
				t.Errorf("valid = %v, want %v; reasons: %v", len(reasons) == 0, tt.wantValid, reasons)
			}
		})
	}
}
