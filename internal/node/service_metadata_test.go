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
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/agentmesh/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func metadataOf(t *testing.T, r *ServiceRegistry, name string) map[string]string {
	t.Helper()
	for _, info := range r.List(api.ServiceType_SERVICE_TYPE_UNSPECIFIED) {
		if info.Name == name {
			return info.GetMetadata()
		}
	}
	t.Fatalf("%s not listed", name)
	return nil
}

// Registration carries the first metadata; SetMetadata replaces it on what
// List answers without touching the service's own Info().
func TestServiceRegistry_Metadata(t *testing.T) {
	r := newServiceRegistryForTest(&fakeDHT{})
	svc := newFakeSvc("agent", api.ServiceType_SERVICE_TYPE_A2A)
	svc.info.Metadata = map[string]string{"team": "web"}
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if got := metadataOf(t, r, "agent"); !maps.Equal(got, map[string]string{"team": "web"}) {
		t.Fatalf("after register: %v", got)
	}

	drain := map[string]string{"example.mark.drain": `{"reason":"reboot"}`}
	changed, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", drain)
	if err != nil || !changed {
		t.Fatalf("SetMetadata: changed=%v err=%v", changed, err)
	}
	drain["example.mark.drain"] = "changed by the caller afterwards"
	got := metadataOf(t, r, "agent")
	if !maps.Equal(got, map[string]string{"example.mark.drain": `{"reason":"reboot"}`}) {
		t.Fatalf("after SetMetadata: %v", got)
	}
	got["mutated"] = "by a reader"
	if again := metadataOf(t, r, "agent"); len(again) != 1 {
		t.Fatalf("a reader changed the registry's metadata: %v", again)
	}
	if !maps.Equal(svc.info.Metadata, map[string]string{"team": "web"}) {
		t.Fatalf("the service's own Info() changed: %v", svc.info.Metadata)
	}

	if changed, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", map[string]string{"example.mark.drain": `{"reason":"reboot"}`}); err != nil || changed {
		t.Fatalf("same metadata again: changed=%v err=%v", changed, err)
	}
	if changed, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", nil); err != nil || !changed {
		t.Fatalf("clear: changed=%v err=%v", changed, err)
	}
	if got := metadataOf(t, r, "agent"); got != nil {
		t.Fatalf("after clearing: %v", got)
	}

	// Registering again starts from what the registration carries.
	if _, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", drain); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if got := metadataOf(t, r, "agent"); !maps.Equal(got, map[string]string{"team": "web"}) {
		t.Fatalf("after registering again: %v", got)
	}

	// Unregistering forgets it.
	if _, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", drain); err != nil {
		t.Fatal(err)
	}
	if err := r.Unregister(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	if len(r.infos) != 0 {
		t.Fatalf("info kept after unregister: %v", r.infos)
	}
}

// The registry answers from its own copy of the registration: a caller
// reusing its ServiceInfo after Register returns changes nothing and
// races with nothing (run with -race).
func TestServiceRegistry_OwnsRegisteredInfo(t *testing.T) {
	r := newServiceRegistryForTest(&fakeDHT{})
	svc := newFakeSvc("agent", api.ServiceType_SERVICE_TYPE_A2A)
	svc.info.Metadata = map[string]string{"team": "web"}
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			svc.info.Metadata["team"] = fmt.Sprint(i)
		}
	}()
	for range 1000 {
		if got := metadataOf(t, r, "agent"); got["team"] != "web" {
			t.Fatalf("listed the caller's map: %v", got)
		}
	}
	<-done
}

// Registering again with other metadata asks for an early report, like
// SetMetadata; registering with the same metadata does not.
func TestServiceRegistry_MetadataChangeCallback(t *testing.T) {
	r := newServiceRegistryForTest(&fakeDHT{})
	calls := 0
	r.onMetadataChange = func() { calls++ }
	svc := newFakeSvc("agent", api.ServiceType_SERVICE_TYPE_A2A)
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("registering without metadata called back %d times", calls)
	}
	if _, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", map[string]string{"example.mark.drain": "{}"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("SetMetadata: %d calls, want 1", calls)
	}
	// The registration carries no metadata, so registering again clears
	// the drain mark: a change.
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("registering again over a mark: %d calls, want 2", calls)
	}
	if err := r.Register(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("registering again unchanged: %d calls, want 2", calls)
	}
}

func TestServiceRegistry_MetadataRefusals(t *testing.T) {
	r := newServiceRegistryForTest(&fakeDHT{})
	bad := newFakeSvc("bad", api.ServiceType_SERVICE_TYPE_A2A)
	bad.info.Metadata = map[string]string{"bad key": "v"}
	if err := r.Register(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "metadata key") {
		t.Fatalf("Register with bad metadata: %v", err)
	}
	if _, ok := r.Get("bad"); ok {
		t.Fatal("service with bad metadata was registered")
	}

	if err := r.Register(context.Background(), newFakeSvc("agent", api.ServiceType_SERVICE_TYPE_A2A)); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"unknown name": func() error {
			_, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "other", map[string]string{"k": "v"})
			return err
		},
		"other type": func() error {
			_, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_MCP, "agent", map[string]string{"k": "v"})
			return err
		},
		"invalid": func() error {
			_, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "agent", map[string]string{"k": "a\nb"})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := metadataOf(t, r, "agent"); got != nil {
		t.Fatalf("refused calls changed metadata: %v", got)
	}
}

// list_local_services, which discovery by type reads on each provider,
// answers with the current metadata.
func TestListLocalServicesCarriesMetadata(t *testing.T) {
	node := newCatalogTestNode(t, "")
	if err := node.SetServiceMetadata(api.ServiceType_SERVICE_TYPE_MCP, "calc", map[string]string{"note": "moving tonight"}); err != nil {
		t.Fatal(err)
	}
	res, _, err := node.handleListLocalServices(context.Background(), &mcp.CallToolRequest{}, ListLocalServicesParams{})
	if err != nil {
		t.Fatal(err)
	}
	var services []*api.ServiceInfo
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &services); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].GetMetadata()["note"] != "moving tonight" {
		t.Fatalf("listed: %v", services)
	}
}

// A metadata change reports the catalog early, carrying the new metadata;
// setting the same metadata again does not.
func TestServiceMetadataChangeReportsEarly(t *testing.T) {
	url, reports := catalogReports(t)
	node := newCatalogTestNode(t, url)
	node.SetIdentityCache([]byte("biscuit"))
	node.catalogReportTrigger = make(chan struct{}, 1)
	node.services.onMetadataChange = node.catalogReportSoon
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node.startCatalogReportLoop(ctx, 10*time.Millisecond, 3*time.Second, 50*time.Millisecond)
	if first := nextReport(t, reports, 5*time.Second); len(first.GetServices()[0].GetMetadata()) != 0 {
		t.Fatalf("first report: %v", first)
	}

	md := map[string]string{"note": "moving tonight"}
	if err := node.SetServiceMetadata(api.ServiceType_SERVICE_TYPE_MCP, "calc", md); err != nil {
		t.Fatal(err)
	}
	if early := nextReport(t, reports, 2*time.Second); !maps.Equal(early.GetServices()[0].GetMetadata(), md) {
		t.Fatalf("early report: %v", early)
	}
	if err := node.SetServiceMetadata(api.ServiceType_SERVICE_TYPE_MCP, "calc", md); err != nil {
		t.Fatal(err)
	}
	noReport(t, reports, 500*time.Millisecond)
	if err := node.SetServiceMetadata(api.ServiceType_SERVICE_TYPE_A2A, "calc", md); err == nil {
		t.Fatal("metadata for a service of another type was accepted")
	}
}
