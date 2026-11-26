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
	"slices"
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

// A request reused for a second registration leaves the first intact: the
// node copies it, and the registry looks services up by its own key.
func TestRegisterServiceReusedRequest(t *testing.T) {
	ctx := context.Background()
	node, cleanup := startBareNode(t, ctx)
	defer cleanup()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer backend.Close()

	req := &api.RegisterServiceRequest{
		Service: &api.ServiceInfo{Type: api.ServiceType_SERVICE_TYPE_A2A, Name: "first", Metadata: map[string]string{"n": "1"}},
		Backend: &api.RegisterServiceRequest_TargetUrl{TargetUrl: backend.URL},
	}
	if err := node.RegisterService(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.Service.Name = "second"
	req.Service.Metadata["n"] = "2"
	if err := node.RegisterService(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, info := range node.ListLocalServices(api.ServiceType_SERVICE_TYPE_A2A) {
		got[info.GetName()] = info.GetMetadata()["n"]
	}
	if !maps.Equal(got, map[string]string{"first": "1", "second": "2"}) {
		t.Fatalf("listed: %v", got)
	}
	if svc, ok := node.services.GetTyped(api.ServiceType_SERVICE_TYPE_A2A, "first"); !ok || svc.Info().GetName() != "first" {
		t.Fatalf("first: %v %v", svc, ok)
	}
}

// The same through the registry alone, for a Service whose Info() its
// caller keeps changing: the registry answers by its own key and copy.
func TestServiceRegistry_SharedInfoPointer(t *testing.T) {
	r := newServiceRegistryForTest(&fakeDHT{})
	info := &api.ServiceInfo{Type: api.ServiceType_SERVICE_TYPE_A2A, Name: "first"}
	if err := r.Register(context.Background(), &fakeService{info: info}); err != nil {
		t.Fatal(err)
	}
	info.Name = "second"
	if err := r.Register(context.Background(), &fakeService{info: info}); err != nil {
		t.Fatal(err)
	}
	info.Type = api.ServiceType_SERVICE_TYPE_MCP
	var names []string
	for _, i := range r.List(api.ServiceType_SERVICE_TYPE_A2A) {
		names = append(names, i.GetName())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"first", "second"}) {
		t.Fatalf("listed: %v", names)
	}
	if _, ok := r.GetTyped(api.ServiceType_SERVICE_TYPE_A2A, "first"); !ok {
		t.Fatal("GetTyped(first) missed the registry's own type")
	}
	if _, err := r.SetMetadata(api.ServiceType_SERVICE_TYPE_A2A, "first", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
}

// bigCatalog is the most services a report may carry, each with a 1 KiB
// description and metadata at its limit: over the report limit with the
// metadata, under it without.
func bigCatalog() ([]*api.ServiceInfo, map[string]string) {
	md := map[string]string{}
	for i := range api.MaxServiceMetadataEntries {
		md[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", api.MaxServiceMetadataBytes/api.MaxServiceMetadataEntries-3)
	}
	var services []*api.ServiceInfo
	for i := range 512 {
		services = append(services, &api.ServiceInfo{Type: api.ServiceType_SERVICE_TYPE_A2A, Name: fmt.Sprintf("agent-%03d", i),
			Description: strings.Repeat("d", 1024), Metadata: maps.Clone(md)})
	}
	return services, md
}

func TestFitCatalogReport(t *testing.T) {
	services, md := bigCatalog()
	if err := api.ValidateServiceMetadata(md); err != nil {
		t.Fatal(err)
	}
	if size := proto.Size(&api.NodeCatalogReport{Services: services}); size <= api.MaxCatalogReportBytes {
		t.Fatalf("test catalog is only %d bytes", size)
	}
	fitted := fitCatalogReport(&api.NodeCatalogReport{Services: services})
	if size := proto.Size(fitted); size > api.MaxCatalogReportBytes {
		t.Fatalf("fitted report is %d bytes", size)
	}
	if !fitted.GetMetadataOmitted() {
		t.Fatal("fitted report does not say metadata was left out")
	}
	for _, svc := range fitted.GetServices() {
		if len(svc.GetMetadata()) != 0 || svc.GetDescription() == "" {
			t.Fatalf("fitted service: %v", svc)
		}
	}

	small := &api.NodeCatalogReport{Services: []*api.ServiceInfo{{Type: api.ServiceType_SERVICE_TYPE_A2A, Name: "a", Metadata: maps.Clone(md)}}}
	if got := fitCatalogReport(small); !maps.Equal(got.GetServices()[0].GetMetadata(), md) || got.GetMetadataOmitted() {
		t.Fatalf("a report that fits changed: %v", got)
	}

	// Too large without any metadata to leave out: nothing was omitted.
	bare := &api.NodeCatalogReport{}
	for _, svc := range services {
		bare.Services = append(bare.Services, &api.ServiceInfo{Type: svc.GetType(), Name: svc.GetName(), Description: strings.Repeat("d", 4096)})
	}
	if got := fitCatalogReport(bare); got.GetMetadataOmitted() {
		t.Fatal("a report with no metadata says metadata was left out")
	}
}

// The node's report leaves metadata out when it would not fit, and still
// carries every service.
func TestReportNodeCatalogLeavesOutMetadataThatDoesNotFit(t *testing.T) {
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
	services, _ := bigCatalog()
	for _, info := range services {
		node.services.insertService(&fakeService{info: info})
	}
	if err := node.reportNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := <-reports
	if len(report.GetServices()) != 513 || !report.GetMetadataOmitted() {
		t.Fatalf("report has %d services, metadata_omitted %v", len(report.GetServices()), report.GetMetadataOmitted())
	}
	if size := proto.Size(report); size > api.MaxCatalogReportBytes {
		t.Fatalf("report is %d bytes", size)
	}
	for _, svc := range report.GetServices() {
		if len(svc.GetMetadata()) != 0 {
			t.Fatalf("metadata kept: %v", svc)
		}
	}
	if md := metadataOf(t, node.services, "agent-000"); len(md) == 0 {
		t.Fatal("metadata gone from the registry")
	}
}

// A catalog of exactly api.MaxCatalogReportBytes without metadata, once
// its services gain metadata, is fitted back to at most that size plus the
// flag's room, which the control plane takes.
func TestFitCatalogReportKeepsAFullCatalogInRoom(t *testing.T) {
	report := &api.NodeCatalogReport{}
	for i := range 512 {
		report.Services = append(report.Services, &api.ServiceInfo{Type: api.ServiceType_SERVICE_TYPE_A2A, Name: fmt.Sprintf("agent-%03d", i), Description: "d"})
	}
	last := report.Services[len(report.Services)-1]
	for proto.Size(report) < api.MaxCatalogReportBytes {
		grow := api.MaxCatalogReportBytes - proto.Size(report)
		if grow > 1024 {
			grow -= 8
		} else {
			grow = 1
		}
		last.Description += strings.Repeat("d", grow)
	}
	if proto.Size(report) != api.MaxCatalogReportBytes {
		t.Fatalf("catalog is %d bytes", proto.Size(report))
	}
	for _, svc := range report.Services {
		svc.Metadata = map[string]string{"example.mark.drain": `{"reason":"reboot"}`}
	}
	fitted := fitCatalogReport(report)
	if !fitted.GetMetadataOmitted() {
		t.Fatal("metadata kept in a report over the limit")
	}
	if size := proto.Size(fitted); size > api.MaxCatalogReportBytes+api.CatalogReportFlagBytes {
		t.Fatalf("fitted report is %d bytes, room is %d", size, api.MaxCatalogReportBytes+api.CatalogReportFlagBytes)
	}
}
