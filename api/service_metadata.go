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
	"unicode"
	"unicode/utf8"
)

// Service metadata (ServiceInfo.metadata) is owner-supplied key/value data
// about one service, such as a maintenance note. Agent Mesh carries it but
// never interprets it, so the bounds only keep it cheap to carry in
// list_local_services answers and catalog reports.
const (
	// MaxServiceMetadataEntries bounds the number of keys on one service.
	MaxServiceMetadataEntries = 32
	// MaxServiceMetadataValueLen bounds one value, in bytes.
	MaxServiceMetadataValueLen = 1024
	// MaxServiceMetadataBytes bounds all keys and values of one service
	// together, in bytes.
	MaxServiceMetadataBytes = 8 * 1024
)

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
