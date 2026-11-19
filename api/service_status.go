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

import "fmt"

// ValidateServiceStatus checks a status a node reports (agentiscale fork):
// its times must be valid Timestamps, which JSON can render (years 1 to
// 9999). A nil status is valid.
func ValidateServiceStatus(st *ServiceStatus) error {
	if st == nil {
		return nil
	}
	if st.Since != nil {
		if err := st.Since.CheckValid(); err != nil {
			return fmt.Errorf("status since: %w", err)
		}
	}
	if st.InputRequiredSince != nil {
		if err := st.InputRequiredSince.CheckValid(); err != nil {
			return fmt.Errorf("status input_required_since: %w", err)
		}
	}
	return nil
}
