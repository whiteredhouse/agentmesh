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
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
)

// Service metadata (ServiceInfo.metadata) is owner-supplied key/value data
// about one service, such as a maintenance note. Agent Mesh carries it but
// never interprets it. The bounds are checked when metadata is set, never
// trimmed later, and the control plane reserves room for the most a
// catalog report can carry (MaxCatalogMetadataBytes).
const (
	// MaxServiceMetadataEntries bounds the number of keys on one service.
	MaxServiceMetadataEntries = 16
	// MaxServiceMetadataValueLen bounds one value, in bytes.
	MaxServiceMetadataValueLen = 512
	// MaxServiceMetadataBytes bounds all keys and values of one service
	// together, in bytes.
	MaxServiceMetadataBytes = 1024
)

// MaxCatalogReportBytes is the room POST /nodes/catalog gives a report for
// everything but service metadata: services, descriptions and statuses.
const MaxCatalogReportBytes = 1 << 20

// MaxCatalogServices is the most services one catalog report may carry.
const MaxCatalogServices = 512

// maxServiceMetadataEntryOverhead is the most protobuf framing one metadata
// entry adds beyond its key and value bytes: the map field's tag (up to 2
// bytes) and length, and the key's and value's tags and lengths.
const maxServiceMetadataEntryOverhead = 2 + 2 + 1 + 1 + 1 + 2

// MaxCatalogMetadataBytes is the most a catalog report's service metadata
// can add to it, serialized: every service at MaxServiceMetadataBytes with
// MaxServiceMetadataEntries entries. POST /nodes/catalog takes this much on
// top of MaxCatalogReportBytes, so a report that fit before metadata always
// fits with it, and a node never has to drop or trim metadata to report.
const MaxCatalogMetadataBytes = MaxCatalogServices * (MaxServiceMetadataBytes + MaxServiceMetadataEntries*maxServiceMetadataEntryOverhead)

// MaxCatalogRequestBytes is the most POST /nodes/catalog reads: the room
// for everything but metadata plus the room for metadata. A receiver also
// checks CatalogReportBaseSize against MaxCatalogReportBytes, so fields
// other than metadata never use the metadata's room.
const MaxCatalogRequestBytes = MaxCatalogReportBytes + MaxCatalogMetadataBytes

// CatalogReportBaseSize is the serialized size of report without its
// services' metadata: what MaxCatalogReportBytes bounds. It measures a copy
// and never changes report, so it is safe while other goroutines read report
// and when several entries share one ServiceInfo.
func CatalogReportBaseSize(report *NodeCatalogReport) int {
	base, ok := proto.Clone(report).(*NodeCatalogReport)
	if !ok || base == nil {
		return 0
	}
	for _, svc := range base.GetServices() {
		if svc != nil {
			svc.Metadata = nil
		}
	}
	return proto.Size(base)
}

// ValidateServiceMetadata checks a service's metadata: at most
// MaxServiceMetadataEntries keys, each a valid label key (1-63 chars of
// [a-zA-Z0-9_.-], see ValidateLabelKey), and values of valid UTF-8 without
// control characters, each at most MaxServiceMetadataValueLen bytes and all
// together at most MaxServiceMetadataBytes. Empty values are allowed. A nil
// or empty map is valid.
func ValidateServiceMetadata(md map[string]string) error {
	if len(md) > MaxServiceMetadataEntries {
		return fmt.Errorf("service metadata has %d keys (max %d)", len(md), MaxServiceMetadataEntries)
	}
	total := 0
	for k, v := range md {
		if !labelKeySyntax.MatchString(k) {
			return fmt.Errorf("invalid service metadata key %q: must be 1-63 chars of [a-zA-Z0-9_.-]", k)
		}
		if len(v) > MaxServiceMetadataValueLen {
			return fmt.Errorf("service metadata %q: value is %d bytes (max %d)", k, len(v), MaxServiceMetadataValueLen)
		}
		if !utf8.ValidString(v) {
			return fmt.Errorf("service metadata %q: value is not valid UTF-8", k)
		}
		for _, r := range v {
			if unicode.IsControl(r) {
				return fmt.Errorf("service metadata %q: value has a control character", k)
			}
		}
		total += len(k) + len(v)
	}
	if total > MaxServiceMetadataBytes {
		return fmt.Errorf("service metadata is %d bytes (max %d)", total, MaxServiceMetadataBytes)
	}
	return nil
}

// ServiceMetadata is service metadata as a config file or a JSON document
// holds it. Decoding refuses a null value instead of reading it as "": a key
// that is present must carry a string, so a caller that tests for a key
// never mistakes a malformed entry for one set on purpose. An explicit ""
// stays "".
type ServiceMetadata map[string]string

// UnmarshalYAML refuses null values (yaml.v2's Unmarshaler).
func (m *ServiceMetadata) UnmarshalYAML(unmarshal func(any) error) error {
	var raw map[string]*string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	md, err := nonNullMetadata(raw)
	*m = md
	return err
}

// UnmarshalJSON refuses null values.
func (m *ServiceMetadata) UnmarshalJSON(data []byte) error {
	var raw map[string]*string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	md, err := nonNullMetadata(raw)
	*m = md
	return err
}

// DecodeServiceMetadataJSON decodes metadata a JSON document carries (a
// JSON null or absent value is no metadata) and checks it: a string map
// without null values, within ValidateServiceMetadata's bounds.
func DecodeServiceMetadataJSON(data []byte) (map[string]string, error) {
	var md ServiceMetadata
	if len(data) > 0 {
		if err := json.Unmarshal(data, &md); err != nil {
			return nil, err
		}
	}
	if err := ValidateServiceMetadata(md); err != nil {
		return nil, err
	}
	if len(md) == 0 {
		return nil, nil
	}
	return md, nil
}

func nonNullMetadata(raw map[string]*string) (ServiceMetadata, error) {
	if raw == nil {
		return nil, nil
	}
	md := make(ServiceMetadata, len(raw))
	for k, v := range raw {
		if v == nil {
			return nil, fmt.Errorf("service metadata key %q has a null value", k)
		}
		md[k] = *v
	}
	return md, nil
}
