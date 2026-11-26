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
	"time"

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

// Discovery hands on only metadata a node could have set: a remote peer's
// invalid or malformed metadata is dropped, every service stays listed, and
// null entries are skipped.
func TestDecodeRemoteCatalog(t *testing.T) {
	tooMany := map[string]string{}
	for i := range api.MaxServiceMetadataEntries + 1 {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	tooManyJSON, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	text := `[
		{"type":1,"name":"good","description":"d","metadata":{"example.mark.drain":"reboot"}},
		null,
		{"type":1,"name":"too-many","metadata":` + string(tooManyJSON) + `},
		{"type":1,"name":"bad-key","metadata":{"bad key":"v"}},
		{"type":1,"name":"not-string","metadata":{"k":7}},
		{"type":1,"name":"not-map","metadata":["k"]},
		{"type":1,"name":"null-value","metadata":{"flag":null}},
		{"type":1,"name":"null-map","metadata":null},
		{"type":1,"name":"empty-value","metadata":{"flag":""}},
		{"type":1,"name":"none"}
	]`
	got, err := decodeRemoteCatalog("peer", text)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var names []string
	for _, svc := range got {
		names = append(names, svc.GetName())
	}
	want := []string{"good", "too-many", "bad-key", "not-string", "not-map", "null-value", "null-map", "empty-value", "none"}
	if !slices.Equal(names, want) {
		t.Fatalf("services %v, want %v", names, want)
	}
	if got[0].GetMetadata()["example.mark.drain"] != "reboot" || got[0].GetDescription() != "d" || got[0].GetType() != api.ServiceType(1) {
		t.Fatalf("valid service decoded wrong: %v", got[0])
	}
	if v, ok := got[7].GetMetadata()["flag"]; !ok || v != "" {
		t.Fatalf("explicit empty value lost: %v", got[7].GetMetadata())
	}
	for _, svc := range append(got[1:7:7], got[8]) {
		if svc.GetMetadata() != nil {
			t.Fatalf("%s kept metadata %v", svc.GetName(), svc.GetMetadata())
		}
	}
	// What a node's own list_local_services writes decodes back the same.
	own := []*api.ServiceInfo{{Type: api.ServiceType(1), Name: "a", Metadata: map[string]string{"k": "v"}}}
	b, err := json.Marshal(own)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeRemoteCatalog("peer", string(b))
	if err != nil || len(back) != 1 || !proto.Equal(back[0], own[0]) {
		t.Fatalf("round trip: %v, %v", back, err)
	}
}
