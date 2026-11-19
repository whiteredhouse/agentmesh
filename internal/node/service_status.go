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

// serviceStatusKey names a local service in the status map.
type serviceStatusKey struct {
	serviceType api.ServiceType
	name        string
}

// SetServiceStatus records what a local service is doing now, which the
// node's catalog reports carry (never its discovery records). nil forgets
// it. A change of state or of the number of tasks waiting for input sends a
// catalog report early, no sooner than CatalogReportMinGap after the last
// one; a change of the times alone waits for the next regular report. A
// status with a time JSON cannot render is refused, as the control plane
// would refuse the whole report.
func (n *AgentMeshNode) SetServiceStatus(serviceType api.ServiceType, name string, status *api.ServiceStatus) error {
	if err := api.ValidateServiceStatus(status); err != nil {
		return err
	}
	key := serviceStatusKey{serviceType, name}
	n.serviceStatusMu.Lock()
	old := n.serviceStatus[key]
	if status == nil {
		delete(n.serviceStatus, key)
	} else {
		if n.serviceStatus == nil {
			n.serviceStatus = map[serviceStatusKey]*api.ServiceStatus{}
		}
		n.serviceStatus[key] = proto.Clone(status).(*api.ServiceStatus)
	}
	n.serviceStatusMu.Unlock()
	if (old == nil) != (status == nil) || old.GetState() != status.GetState() || old.GetInputRequired() != status.GetInputRequired() {
		select {
		case n.catalogReportTrigger <- struct{}{}:
		default:
		}
	}
	return nil
}

// serviceStatuses returns the recorded status of each of services, in their
// order. A status for a service that isn't registered is left out.
func (n *AgentMeshNode) serviceStatuses(services []*api.ServiceInfo) []*api.ServiceStatusReport {
	n.serviceStatusMu.Lock()
	defer n.serviceStatusMu.Unlock()
	var out []*api.ServiceStatusReport
	for _, svc := range services {
		if status, ok := n.serviceStatus[serviceStatusKey{svc.GetType(), svc.GetName()}]; ok {
			out = append(out, &api.ServiceStatusReport{Type: svc.GetType(), Name: svc.GetName(), Status: proto.Clone(status).(*api.ServiceStatus)})
		}
	}
	return out
}
