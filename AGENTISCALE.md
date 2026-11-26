# agentiscale's fork of Google Agent Mesh

This branch (`agentiscale`) is [google/agentmesh](https://github.com/google/agentmesh)
release **v0.2.0** (commit `5da6337`) plus the commits below. agentiscaled embeds the
node from it. `main` stays a copy of upstream; changes land on `agentiscale` through
pull requests. Licensed under Apache-2.0; see [LICENSE](LICENSE).

agentiscaled keeps Google's module path and pins this branch with
`replace github.com/google/agentmesh => github.com/whiteredhouse/agentmesh <version>`.

## What differs from upstream

| Change | Why |
|---|---|
| `internal/controlplane/bound_peer.go` and its test, plus two calls in `internal/controlplane/server.go` (marked "agentiscale fork") | A bootstrap token whose description starts with `agentiscale-bound-peer:<peer ID>` admits only that peer: `/enroll` refuses any other key before issuing a credential, and `/admin/bootstrap-tokens` refuses an invalid peer ID. Upstream tokens admit any key, so a node could hand its token to another. The binding lives in the description, so no schema or API change is needed. |
| Router member credentials cache owned serialized bytes and decode a separate Biscuit for each authorization | The Biscuit library can mutate a parsed token's backing buffers while creating an authorizer. Directory requests, ingress target facts, the provider credentials the router caches for ingress, the revocation sweep and the handshake's role check must not share that state. `internal/router/member_credential_test.go` exercises concurrent authorization. `grep -rn cred.biscuit internal/router` must find nothing. |
| `export/node/` | Upstream keeps the node in `internal/node`, which other modules cannot import. `export/node` re-exports the names agentiscaled uses as type and function aliases. It adds no behaviour. |
| `export/standalone/` | Upstream keeps the all-in-one mesh (control plane and router, as `agentmesh-one` runs it) in `internal/standalone`. `export/standalone` re-exports it as aliases, so agentiscaled's tests can start a mesh of their own and run nodes that call each other through it. It adds no behaviour. |
| `internal/node/libp2p_transport.go` | go-libp2p-http uses the request context only to open the stream, so a cancelled request through the egress proxy left the remote stream open. The transport now resets the stream when the request ends. Candidate for upstream. |
| `internal/node/controlplane.go` (`mergeTrustedKeys`), `internal/node/node.go` (`pruneTrustedKeys`, `NewAgentMeshNode`, `Start`, `StaleIdentityError`) and their tests; `StaleIdentityError` exported in `export/node` | Upstream ages a trusted key from when the node first saw it and keeps only the newest past the grace period, so a node could drop a key `/keys` still lists, even the one that signed its own identity, and fail its next start with `biscuit: invalid signature`. Keys now age from the last `/keys` answer that listed them, every key with the latest time is kept, the identity's signer is never pruned, and the configured key re-added at a start keeps an old time. `Start` returns a typed error that unwraps to the refresh error, so agentiscaled can tell a refusal from no answer. Upstream: issue google/agentmesh#916, PR google/agentmesh#917; drop this row when an upstream release includes it. |
| `ServiceStatus`, `ServiceState`, `ServiceStatusReport` and `service_statuses = 1000` on `NodeCatalogReport` and `NodeServices` in `api/agentmesh.proto`; `api/service_status.go` (`ValidateServiceStatus`, `ValidateServiceStatusReports`), `internal/node/service_status.go` (`SetServiceStatus`) and their tests; `client.ReportCatalogWithStatus`; a check and the status cache in `internal/controlplane/catalog.go`; the catalog loop in `internal/node/node.go` takes an early-report trigger; `Options.CatalogReportMinGap` | A node can say what each local service is doing now (idle or busy, and how many tasks wait for their caller's input), so an owner's console can show it. Statuses ride only in catalog reports (`POST /nodes/catalog`) and the control plane's view of them, as their own list keyed by service type and name; `ServiceInfo`, which registration and discovery share, is unchanged, so discovery can't carry a status. Field numbers from 1000 stay clear of upstream. A change of state or of waiting tasks sends a report early, at most one per `CatalogReportMinGap` (5 seconds by default); an early report never pushes back the regular one. Statuses carry no request content. `api.ValidateServiceStatusReports` refuses a status without a service and times JSON cannot render (past year 9999); the control plane checks it before caching a report and the node before keeping a status, so one report can never break `/admin/status`. Candidate for upstream. |
| `metadata = 1000` on `ServiceInfo` and `srv_metadata = 1000` on `DiscoveredProvider` in `api/agentmesh.proto`; `api/service_metadata.go` (`ValidateServiceMetadata`); `ServiceRegistry.SetMetadata` and the registry's metadata map in `internal/node/service_registry.go`; `internal/node/service_metadata.go` (`SetServiceMetadata`); `metadata` in `ServiceConfig`; discovery by type copies it into `DiscoveredProvider`; a check in `internal/controlplane/catalog.go`; and their tests | A service can carry owner-supplied key/value metadata (agentiscaled puts an agent's marks there, such as a drain with its reason) that `list_local_services`, discovery by type and catalog reports carry and Agent Mesh never acts on. Discovery by type reads each provider's live catalog, so a change shows on the next lookup without announcing anything again; a change also sends a catalog report early. Registering again resets it, and a registration that changes it also reports early. Bounds, checked when metadata is set and never trimmed later: 16 keys of label-key syntax, values of UTF-8 without control characters up to 512 bytes, 1 KiB in all. `POST /nodes/catalog` takes `api.MaxCatalogReportBytes` (1 MiB, as before) plus `api.MaxCatalogMetadataBytes` (the most metadata 512 services can carry), so a report that fit before metadata always fits with it. The node copies each registration request and the registry keeps its own copy and key for each service. Unlike node labels, metadata is not attested by the control plane. Field numbers from 1000 stay clear of upstream; the upstream proposal uses 5. Candidate for upstream. |
| This file | Records the fork. |

## Updating to a new upstream release

1. Sync `main` with upstream, then merge the new release tag into `agentiscale`
   in a pull request. Merge rather than rebase, so that commits agentiscaled
   pinned earlier stay on the branch.
2. Run `go build ./... && go vet ./export/...` and
   `go test -race ./internal/router ./internal/identity ./internal/controlplane ./internal/node`.
   If an aliased name was renamed or removed upstream, fix `export/node/node.go`
   or `export/standalone/standalone.go`.
   Check new reads of a parsed credential in `internal/router`.
3. Update the release and commit at the top of this file, tag the result
   (for example `v0.3.0-agentiscale.1`) and move the pin in agentiscaled.
