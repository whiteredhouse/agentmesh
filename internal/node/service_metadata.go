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

import "github.com/google/agentmesh/api"

// SetServiceMetadata replaces the metadata (ServiceInfo.metadata) of a
// registered local service; nil or empty clears it. The node's
// list_local_services answer carries it at once, so discovery by type on
// other nodes sees it on their next lookup; the next catalog report carries
// it to the control plane. Agent Mesh never acts on metadata. Registering
// the service again resets its metadata to what the registration carries.
func (n *AgentMeshNode) SetServiceMetadata(serviceType api.ServiceType, name string, metadata map[string]string) error {
	_, err := n.services.SetMetadata(serviceType, name, metadata)
	return err
}
