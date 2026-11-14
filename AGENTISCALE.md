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
