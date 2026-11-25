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
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A time JSON cannot render is refused, so one node's report can never
// break a status response that serializes every report.
func TestValidateServiceStatus(t *testing.T) {
	if err := ValidateServiceStatus(nil); err != nil {
		t.Fatal(err)
	}
	good := &ServiceStatus{State: ServiceState_SERVICE_STATE_BUSY, Since: timestamppb.New(time.Now()), InputRequired: 1, InputRequiredSince: timestamppb.New(time.Now())}
	if err := ValidateServiceStatus(good); err != nil {
		t.Fatal(err)
	}
	if _, err := protojson.Marshal(good); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]*ServiceStatus{
		"since past year 9999":                {Since: &timestamppb.Timestamp{Seconds: 253402300800}},
		"input_required_since past year 9999": {InputRequiredSince: &timestamppb.Timestamp{Seconds: 253402300800}},
		"negative nanos":                      {Since: &timestamppb.Timestamp{Nanos: -1}},
	} {
		if err := ValidateServiceStatus(st); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := protojson.Marshal(st); err == nil {
			t.Errorf("%s: protojson renders it, so the check is stricter than needed", name)
		}
	}
}

// Each reported status names a service of a known type and holds a valid
// status.
func TestValidateServiceStatusReports(t *testing.T) {
	ok := &ServiceStatusReport{Type: ServiceType_SERVICE_TYPE_A2A, Name: "a", Status: &ServiceStatus{State: ServiceState_SERVICE_STATE_IDLE}}
	stopped := &ServiceStatusReport{Type: ServiceType_SERVICE_TYPE_A2A, Name: "b", Status: &ServiceStatus{State: ServiceState_SERVICE_STATE_STOPPED}}
	if err := ValidateServiceStatusReports([]*ServiceStatusReport{ok, stopped}); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*ServiceStatusReport{
		"nil":          nil,
		"no name":      {Type: ServiceType_SERVICE_TYPE_A2A, Status: &ServiceStatus{}},
		"no type":      {Name: "a", Status: &ServiceStatus{}},
		"no status":    {Type: ServiceType_SERVICE_TYPE_A2A, Name: "a"},
		"invalid time": {Type: ServiceType_SERVICE_TYPE_A2A, Name: "a", Status: &ServiceStatus{Since: &timestamppb.Timestamp{Seconds: 253402300800}}},
	} {
		if err := ValidateServiceStatusReports([]*ServiceStatusReport{ok, r}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
