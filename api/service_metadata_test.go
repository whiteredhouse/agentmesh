// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestValidateServiceMetadata(t *testing.T) {
	tooMany := map[string]string{}
	for i := range MaxServiceMetadataEntries + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	tooBig := map[string]string{}
	for i := range 3 {
		tooBig[fmt.Sprintf("k%d", i)] = strings.Repeat("x", MaxServiceMetadataValueLen)
	}
	tests := []struct {
		name string
		md   map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"empty", map[string]string{}, true},
		{"json value", map[string]string{"example.mark.drain": `{"reason":"reboot","since":"2026-01-02T03:04:05Z"}`}, true},
		{"empty value", map[string]string{"flag": ""}, true},
		{"unicode value", map[string]string{"note": "zażółć"}, true},
		{"longest value", map[string]string{"k": strings.Repeat("x", MaxServiceMetadataValueLen)}, true},
		{"most keys", func() map[string]string {
			m := map[string]string{}
			for i := range MaxServiceMetadataEntries {
				m[fmt.Sprintf("k%d", i)] = ""
			}
			return m
		}(), true},
		{"too many keys", tooMany, false},
		{"empty key", map[string]string{"": "v"}, false},
		{"key with space", map[string]string{"a b": "v"}, false},
		{"key with slash", map[string]string{"a/b": "v"}, false},
		{"key too long", map[string]string{strings.Repeat("k", 64): "v"}, false},
		{"value too long", map[string]string{"k": strings.Repeat("x", MaxServiceMetadataValueLen+1)}, false},
		{"invalid utf-8", map[string]string{"k": "\xff"}, false},
		{"newline", map[string]string{"k": "a\nb"}, false},
		{"escape", map[string]string{"k": "\x1b[31m"}, false},
		{"too many bytes", tooBig, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateServiceMetadata(tc.md)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateServiceMetadata: %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// MaxCatalogMetadataBytes holds for any valid metadata: what it adds to a
// serialized ServiceInfo never passes one service's share of the room.
func TestCatalogMetadataRoomCoversAnyValidMetadata(t *testing.T) {
	perService := MaxCatalogMetadataBytes / MaxCatalogServices
	// Measured in a report, so the growth of the service's own length
	// prefix counts too, for services just under each varint boundary
	// (one- and two-byte lengths; a service stays under 2 MiB, so a
	// three-byte length never grows).
	bases := []*ServiceInfo{{Name: "a"}}
	for _, limit := range []int{1 << 7, 1 << 14} {
		svc := &ServiceInfo{Name: "a"}
		svc.Description = strings.Repeat("d", limit-1-proto.Size(svc)-3)
		for proto.Size(svc) < limit-1 {
			svc.Description += "d"
		}
		bases = append(bases, svc)
	}
	added := func(md map[string]string) int {
		most := 0
		for _, base := range bases {
			with := proto.Clone(base).(*ServiceInfo)
			with.Metadata = md
			grew := proto.Size(&NodeCatalogReport{Services: []*ServiceInfo{with}}) - proto.Size(&NodeCatalogReport{Services: []*ServiceInfo{base}})
			most = max(most, grew)
		}
		return most
	}
	shapes := map[string]map[string]string{}
	// Most entries, values long enough for two-byte lengths, bytes at the limit.
	many := map[string]string{}
	for i := range MaxServiceMetadataEntries {
		k := fmt.Sprintf("k%02d", i)
		many[k] = strings.Repeat("v", MaxServiceMetadataBytes/MaxServiceMetadataEntries-len(k))
	}
	shapes["most entries"] = many
	shapes["longest key and value"] = map[string]string{strings.Repeat("k", 63): strings.Repeat("v", MaxServiceMetadataValueLen)}
	two := map[string]string{"a": strings.Repeat("v", MaxServiceMetadataValueLen), "b": strings.Repeat("v", MaxServiceMetadataBytes-MaxServiceMetadataValueLen-2)}
	shapes["two long values"] = two
	// Multi-byte runes count in bytes, as the bounds do.
	shapes["unicode"] = map[string]string{"u": strings.Repeat("ż", MaxServiceMetadataValueLen/2)}
	for name, md := range shapes {
		if err := ValidateServiceMetadata(md); err != nil {
			t.Fatalf("%s: not valid: %v", name, err)
		}
		if got := added(md); got > perService {
			t.Errorf("%s adds %d bytes, share is %d", name, got, perService)
		}
	}
	// Random valid metadata, too.
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		md := map[string]string{}
		left := MaxServiceMetadataBytes
		for range rng.IntN(MaxServiceMetadataEntries + 1) {
			k := fmt.Sprintf("k%d", len(md))
			if left < len(k) {
				break
			}
			n := min(rng.IntN(MaxServiceMetadataValueLen+1), left-len(k))
			md[k] = strings.Repeat("v", n)
			left -= len(k) + n
		}
		if ValidateServiceMetadata(md) != nil {
			continue
		}
		if got := added(md); got > perService {
			t.Fatalf("%v adds %d bytes, share is %d", md, got, perService)
		}
	}
}

// CatalogReportBaseSize is the size without metadata and leaves the report
// as it was.
func TestCatalogReportBaseSize(t *testing.T) {
	md := map[string]string{"example.mark.drain": `{"reason":"reboot"}`}
	report := &NodeCatalogReport{Services: []*ServiceInfo{
		{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a", Description: "first", Metadata: md},
		nil,
		{Type: ServiceType_SERVICE_TYPE_A2A, Name: "b"},
	}}
	stripped := proto.Clone(report).(*NodeCatalogReport)
	for _, svc := range stripped.Services {
		if svc != nil {
			svc.Metadata = nil
		}
	}
	if got, want := CatalogReportBaseSize(report), proto.Size(stripped); got != want {
		t.Fatalf("base size %d, want %d", got, want)
	}
	if got := report.Services[0].GetMetadata(); len(got) != 1 || got["example.mark.drain"] != md["example.mark.drain"] {
		t.Fatalf("metadata changed: %v", got)
	}
	if proto.Size(report) <= CatalogReportBaseSize(report) {
		t.Fatal("metadata added nothing to the size")
	}
}

// Two entries sharing one ServiceInfo must keep its metadata: measuring may
// not write to the report at all.
func TestCatalogReportBaseSizeSharedService(t *testing.T) {
	shared := &ServiceInfo{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a", Metadata: map[string]string{"k": "v"}}
	report := &NodeCatalogReport{Services: []*ServiceInfo{shared, shared}}
	before := proto.Size(report)
	stripped := &NodeCatalogReport{Services: []*ServiceInfo{
		{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a"},
		{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a"},
	}}
	if got, want := CatalogReportBaseSize(report), proto.Size(stripped); got != want {
		t.Fatalf("base size %d, want %d", got, want)
	}
	if shared.GetMetadata()["k"] != "v" || proto.Size(report) != before {
		t.Fatalf("report changed: metadata %v", shared.GetMetadata())
	}
}

// Measuring runs while other goroutines read the same report.
func TestCatalogReportBaseSizeConcurrentReaders(t *testing.T) {
	report := &NodeCatalogReport{Services: []*ServiceInfo{
		{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a", Metadata: map[string]string{"k": "v"}},
	}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() { defer wg.Done(); _ = CatalogReportBaseSize(report) }()
		go func() {
			defer wg.Done()
			if report.Services[0].GetMetadata()["k"] != "v" {
				t.Error("metadata cleared while measuring")
			}
		}()
	}
	wg.Wait()
}
