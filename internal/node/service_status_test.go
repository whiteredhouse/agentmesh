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
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/agentmesh/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// catalogReports serves /nodes/catalog and hands over each report.
func catalogReports(t *testing.T) (string, chan *api.NodeCatalogReport) {
	t.Helper()
	reports := make(chan *api.NodeCatalogReport, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var report api.NodeCatalogReport
		if err := proto.Unmarshal(body, &report); err != nil {
			t.Errorf("decode report: %v", err)
		}
		reports <- &report
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server.URL, reports
}

func nextReport(t *testing.T, reports chan *api.NodeCatalogReport, within time.Duration) *api.NodeCatalogReport {
	t.Helper()
	select {
	case r := <-reports:
		return r
	case <-time.After(within):
		t.Fatalf("no catalog report within %s", within)
		return nil
	}
}

func noReport(t *testing.T, reports chan *api.NodeCatalogReport, during time.Duration) {
	t.Helper()
	select {
	case r := <-reports:
		t.Fatalf("unexpected catalog report: %v", r)
	case <-time.After(during):
	}
}

// A service's status rides in the catalog report only, on a copy: the
// registry, which discovery answers from, never carries it.
func TestServiceStatusInCatalogReport(t *testing.T) {
	url, reports := catalogReports(t)
	node := newCatalogTestNode(t, url)
	node.SetIdentityCache([]byte("biscuit"))
	since := timestamppb.New(time.Unix(1700000000, 0))
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY, Since: since, InputRequired: 2}); err != nil {
		t.Fatal(err)
	}
	// Same name, other type: not the registered service.
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_A2A, "calc", &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_IDLE}); err != nil {
		t.Fatal(err)
	}

	if err := node.reportNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := nextReport(t, reports, time.Second).Services
	if len(got) != 1 || got[0].GetStatus().GetState() != api.ServiceState_SERVICE_STATE_BUSY || got[0].GetStatus().GetInputRequired() != 2 || !proto.Equal(got[0].GetStatus().GetSince(), since) {
		t.Fatalf("reported services: %v", got)
	}
	for _, svc := range node.ListLocalServices(api.ServiceType_SERVICE_TYPE_UNSPECIFIED) {
		if svc.Status != nil {
			t.Fatalf("the registry's record carries a status: %v", svc)
		}
	}

	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", nil); err != nil {
		t.Fatal(err)
	}
	if err := node.reportNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nextReport(t, reports, time.Second).Services; len(got) != 1 || got[0].Status != nil {
		t.Fatalf("after forgetting the status: %v", got)
	}
}

// A change of state or of waiting tasks reports early, no sooner than the
// least gap after the last report; a change of the times alone waits for
// the regular report.
func TestServiceStatusChangeReportsEarly(t *testing.T) {
	url, reports := catalogReports(t)
	node := newCatalogTestNode(t, url)
	node.SetIdentityCache([]byte("biscuit"))
	node.catalogReportTrigger = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const gap = 300 * time.Millisecond
	// The interval only needs to outlast the test; its tenth is the jitter
	// of the first report.
	node.startCatalogReportLoop(ctx, 10*time.Millisecond, 3*time.Second, gap)
	first := nextReport(t, reports, 5*time.Second)
	reported := time.Now()
	if first.Services[0].Status != nil {
		t.Fatalf("first report: %v", first)
	}

	busy := &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY, Since: timestamppb.Now()}
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", busy); err != nil {
		t.Fatal(err)
	}
	early := nextReport(t, reports, 5*time.Second)
	if early.Services[0].GetStatus().GetState() != api.ServiceState_SERVICE_STATE_BUSY {
		t.Fatalf("early report: %v", early)
	}
	if waited := time.Since(reported); waited < gap {
		t.Fatalf("early report came %s after the last one, want at least %s", waited, gap)
	}

	// Only the time moved: nothing to report early.
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY, Since: timestamppb.New(time.Now().Add(time.Second))}); err != nil {
		t.Fatal(err)
	}
	noReport(t, reports, 2*gap)

	busy.InputRequired = 1
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", busy); err != nil {
		t.Fatal(err)
	}
	if got := nextReport(t, reports, 5*time.Second); got.Services[0].GetStatus().GetInputRequired() != 1 {
		t.Fatalf("report after a task began waiting for input: %v", got)
	}
}

// A status time JSON cannot render is refused at the node, as the control
// plane would refuse the whole report.
func TestSetServiceStatusRefusesInvalidTimes(t *testing.T) {
	node := newCatalogTestNode(t, "")
	bad := &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY, Since: &timestamppb.Timestamp{Seconds: 253402300800}}
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", bad); err == nil {
		t.Fatal("accepted a time past year 9999")
	}
	if got := node.withServiceStatus(node.ListLocalServices(api.ServiceType_SERVICE_TYPE_UNSPECIFIED)); got[0].Status != nil {
		t.Fatalf("the refused status was kept: %v", got)
	}
}

// Registration takes no status: discovery and list_local_services answer
// from the registered record, which must never carry one.
func TestRegistrationTakesNoStatus(t *testing.T) {
	_, err := NewServiceFromRequest(&api.RegisterServiceRequest{
		Service: &api.ServiceInfo{Name: "a", Type: api.ServiceType_SERVICE_TYPE_A2A, Status: &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY}},
		Backend: &api.RegisterServiceRequest_TargetUrl{TargetUrl: "http://127.0.0.1:1"},
	})
	if err == nil || !strings.Contains(err.Error(), "SetServiceStatus") {
		t.Fatalf("registration with a status: %v", err)
	}
}

// The registry itself refuses a status, so embedded callers that build a
// Service and call Services().Register can't reach discovery with one.
func TestRegistryRegisterTakesNoStatus(t *testing.T) {
	r := NewServiceRegistry(&fakeDHT{}, 0)
	svc := &A2AService{baseService: baseService{
		info:    &api.ServiceInfo{Name: "a", Type: api.ServiceType_SERVICE_TYPE_A2A, Status: &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY}},
		backend: &api.RegisterServiceRequest_TargetUrl{TargetUrl: "http://127.0.0.1:1"},
	}}
	if err := r.Register(context.Background(), svc); err == nil || !strings.Contains(err.Error(), "SetServiceStatus") {
		t.Fatalf("Register with a status: %v", err)
	}
	if got := r.List(api.ServiceType_SERVICE_TYPE_UNSPECIFIED); len(got) != 0 {
		t.Fatalf("registry lists %v", got)
	}
}

// An early report never pushes back the regular one, also when the least
// gap between early reports is longer than the regular interval.
func TestStatusChangeKeepsTheRegularDeadline(t *testing.T) {
	url, reports := catalogReports(t)
	node := newCatalogTestNode(t, url)
	node.SetIdentityCache([]byte("biscuit"))
	node.catalogReportTrigger = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const interval = 200 * time.Millisecond
	node.startCatalogReportLoop(ctx, 10*time.Millisecond, interval, time.Hour)
	nextReport(t, reports, 5*time.Second)
	reported := time.Now()
	if err := node.SetServiceStatus(api.ServiceType_SERVICE_TYPE_MCP, "calc", &api.ServiceStatus{State: api.ServiceState_SERVICE_STATE_BUSY}); err != nil {
		t.Fatal(err)
	}
	got := nextReport(t, reports, 5*time.Second)
	if waited := time.Since(reported); waited > 2*interval {
		t.Fatalf("the regular report came %s after the last one, want about %s", waited, interval)
	}
	if got.Services[0].GetStatus().GetState() != api.ServiceState_SERVICE_STATE_BUSY {
		t.Fatalf("report: %v", got)
	}
}
