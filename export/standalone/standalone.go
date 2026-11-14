// Copyright 2026 Google LLC
// Modifications copyright 2026 agentiscale.
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

// Package standalone exports the all-in-one mesh (internal/standalone: control
// plane and router in one process, as agentmesh-one runs it) so that a program
// outside this module can start a mesh of its own, such as in a test of nodes
// that call each other.
//
// Like export/node, it adds no behaviour: every name is an alias of the
// internal package.
package standalone

import internal "github.com/google/agentmesh/internal/standalone"

// Options configures a Server (internal/standalone.Options).
type Options = internal.Options

// Server is a running mesh (internal/standalone.Server).
type Server = internal.Server

// New makes a mesh from opts; Start runs it.
var New = internal.New
