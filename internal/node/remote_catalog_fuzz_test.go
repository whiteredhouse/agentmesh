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

package node

import (
	"encoding/json"
	"testing"

	"github.com/google/agentmesh/api"
	"google.golang.org/protobuf/proto"
)

// FuzzDecodeRemoteCatalog holds decodeRemoteCatalog's rules for any remote
// answer: it never panics, never hands on a nil service or metadata outside
// ValidateServiceMetadata's bounds, hands on only metadata values the entry
// carried as strings (a null never becomes ""), and a well-formed entry
// stays listed, unchanged, whatever other entries sit beside it.
func FuzzDecodeRemoteCatalog(f *testing.F) {
	for _, seed := range []string{
		``,
		`null`,
		`{}`,
		`7`,
		`"x"`,
		`[]`,
		`{"type":1,"name":"a"}`,
		`{"type":1,"name":"a","metadata":{"k":"v"}}`,
		`{"type":1,"name":"a","metadata":{"flag":null}}`,
		`{"type":1,"name":"a","metadata":{"flag":""}}`,
		`{"type":1,"name":"a","metadata":{"k":7}}`,
		`{"type":1,"name":"a","metadata":["k"]}`,
		`{"type":1,"name":"a","metadata":{"bad key":"v"}}`,
		`{"type":1,"name":7}`,
		`{"type":"x","name":"a"}`,
		`{"metadata":{"k":"v"}}`,
		`{"type":1,"name":"a","METADATA":{"k":null}}`,
		`{"type":1,"name":"a","metadata":{"k":"v"},"metadata":{"k":null}}`,
	} {
		f.Add(seed)
	}
	good := &api.ServiceInfo{Type: api.ServiceType(1), Name: "good", Description: "d", Metadata: map[string]string{"example.mark.drain": "", "k": "v"}}
	goodJSON, err := json.Marshal(good)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, entry string) {
		check := func(text string) []*api.ServiceInfo {
			got, err := decodeRemoteCatalog("peer", text)
			if err != nil {
				return nil
			}
			for _, svc := range got {
				if svc == nil {
					t.Fatalf("%q: nil service", text)
				}
				if err := api.ValidateServiceMetadata(svc.GetMetadata()); err != nil {
					t.Fatalf("%q: metadata %v handed on: %v", text, svc.GetMetadata(), err)
				}
			}
			return got
		}
		// The fuzzed text as a whole answer.
		check(entry)
		// As a single entry, every metadata value handed on is a string the
		// entry itself carried under that key: nothing is invented, and a
		// null never becomes "".
		if got := check("[" + entry + "]"); len(got) == 1 && got[0].GetMetadata() != nil {
			var held remoteCatalogEntry
			var sent map[string]any
			if json.Unmarshal([]byte(entry), &held) != nil || json.Unmarshal(held.Metadata, &sent) != nil {
				t.Fatalf("%q: metadata %v handed on from an entry that doesn't decode", entry, got[0].GetMetadata())
			}
			for k, v := range got[0].GetMetadata() {
				if s, ok := sent[k].(string); !ok || s != v {
					t.Fatalf("%q: metadata %s=%q, entry carried %v", entry, k, v, sent[k])
				}
			}
		}
		// The fuzzed text as one entry beside a well-formed one: the
		// well-formed entry must stay listed and unchanged.
		if !json.Valid([]byte(entry)) {
			return
		}
		for _, text := range []string{"[" + string(goodJSON) + "," + entry + "]", "[" + entry + "," + string(goodJSON) + "]"} {
			got := check(text)
			found := false
			for _, svc := range got {
				if svc.GetName() == "good" && proto.Equal(svc, good) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q: well-formed entry lost: %v", text, got)
			}
		}
	})
}
