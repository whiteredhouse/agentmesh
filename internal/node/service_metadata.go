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
	"github.com/google/agentmesh/api"
	"google.golang.org/protobuf/proto"
)

// SetServiceMetadata replaces the metadata (ServiceInfo.metadata) of a
// registered local service; nil or empty clears it. The node's
// list_local_services answer carries it at once, so discovery by type on
// other nodes sees it on their next lookup, and a change sends a catalog
// report early, no sooner than CatalogReportMinGap after the last one
// (as does a registration that changes it).
// Agent Mesh never acts on metadata. Registering the service again resets
// its metadata to what the registration carries.
func (n *AgentMeshNode) SetServiceMetadata(serviceType api.ServiceType, name string, metadata map[string]string) error {
	_, err := n.services.SetMetadata(serviceType, name, metadata)
	return err
}

// catalogReportSoon asks the catalog loop for an early report, no sooner
// than CatalogReportMinGap after the last one.
func (n *AgentMeshNode) catalogReportSoon() {
	select {
	case n.catalogReportTrigger <- struct{}{}:
	default:
	}
}

// fitCatalogReport leaves service metadata out of a catalog report that
// would otherwise exceed api.MaxCatalogReportBytes, and says so with
// metadata_omitted, so metadata can never make the control plane refuse a
// report and keep serving stale services and statuses, and a console can
// tell metadata left out from metadata cleared. Metadata is the only part
// a node can drop without saying something false; list_local_services
// still answers with it. The report's services are copies
// (ListLocalServices), so they are changed in place.
func fitCatalogReport(report *api.NodeCatalogReport) *api.NodeCatalogReport {
	if proto.Size(report) <= api.MaxCatalogReportBytes {
		return report
	}
	dropped := 0
	for _, svc := range report.GetServices() {
		if len(svc.GetMetadata()) > 0 {
			svc.Metadata = nil
			dropped++
		}
	}
	if dropped > 0 {
		report.MetadataOmitted = true
		logger.Warnf("[Catalog] Report over %d bytes; leaving out the metadata of %d services", api.MaxCatalogReportBytes, dropped)
	}
	return report
}
