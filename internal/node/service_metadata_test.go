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
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/agentmesh/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"
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

// The catalog report carries each service's current metadata.
func TestServiceMetadataInCatalogReport(t *testing.T) {
	reports := make(chan *api.NodeCatalogReport, 1)
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
	node := newCatalogTestNode(t, server.URL)
	node.SetIdentityCache([]byte("biscuit"))
	md := map[string]string{"note": "moving tonight"}
	if err := node.SetServiceMetadata(api.ServiceType_SERVICE_TYPE_MCP, "calc", md); err != nil {
		t.Fatal(err)
	}
	if err := node.reportNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if report := <-reports; len(report.GetServices()) != 1 || !maps.Equal(report.GetServices()[0].GetMetadata(), md) {
		t.Fatalf("reported: %v", report)
	}
}
