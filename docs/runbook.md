# Run the local application API

The API starts a local HTTP process after two read-only Kubernetes checks. It fetches the server version and gets namespace `deploy-api` under one overall 10-second deadline. Health confirms that the HTTP process is serving. It does not recheck Kubernetes.

## Prepare dependencies and the namespace

Use Go 1.26.0. The module pins `k8s.io/api`, `k8s.io/apimachinery`, and `k8s.io/client-go` to `v0.34.1`.

Fetch dependencies and generate `go.sum` in an environment with module access.

```sh
go mod tidy
go test ./...
go vet ./...
go build ./...
```

Check Docker Desktop without changing your current context.

```sh
go version
kubectl --context=docker-desktop version -o json
kubectl --context=docker-desktop get namespace deploy-api
```

If the namespace is absent, create it manually before startup.

```sh
kubectl --context=docker-desktop create namespace deploy-api
```

The API does not create or delete the namespace.

## Start and check the API

Use normal client-go loading rules, including `KUBECONFIG` and the default kubeconfig location.

```sh
go run .
```

To select a file explicitly, use the flag below. An explicit-file failure does not fall back to another file.

```sh
go run . -kubeconfig /absolute/path/to/config
```

Both commands select context `docker-desktop`, regardless of the file's current context. Successful startup logs the context, namespace, and server version, then starts `127.0.0.1:8080`.

In a separate terminal, request health.

```sh
curl --fail-with-body -i http://127.0.0.1:8080/healthz
curl -i -X POST http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/apps
```

Expect `200`, `Content-Type: application/json`, and `{"status":"ok"}` for the first request. Expect `405` for the second request. The third returns `200` with an `apps` array, which is empty when no managed applications exist.

Press Ctrl-C in the API terminal to release the port. Restart and repeat the health request.

## Create an application

Verify the intended public image has `linux/arm64` support before sending it. Record its immutable digest for repeatable cluster exercises. The API accepts the reference as supplied and does not inspect the registry.

```sh
docker buildx imagetools inspect docker.io/library/nginx:stable-alpine
curl --fail-with-body -i -H 'Content-Type: application/json' \
  -d '{"name":"learn-a","image":"docker.io/library/nginx:stable-alpine","containerPort":80}' \
  http://127.0.0.1:8080/apps
```

Expect `202`, `Location: /apps/learn-a`, and this JSON shape.

```json
{"name":"learn-a","namespace":"deploy-api","deployment":"learn-a","service":"learn-a","statusUrl":"/apps/learn-a"}
```

Acceptance means both resource writes succeeded. Kubernetes creates ReplicaSets and Pods afterward. The response does not wait for readiness. The status URL reserves the future inspection endpoint; GET inspection is not implemented yet. Use kubectl to observe this slice.

```sh
kubectl --context=docker-desktop -n deploy-api get deployment learn-a -o yaml
kubectl --context=docker-desktop -n deploy-api get service learn-a -o yaml
kubectl --context=docker-desktop -n deploy-api rollout status deployment/learn-a --timeout=180s
kubectl --context=docker-desktop -n deploy-api port-forward service/learn-a 8081:80
```

Keep forwarding open and run `curl --fail http://127.0.0.1:8081/` in another terminal. Create a second app with a different name and forward its Service to a different local port to check isolation. Resource labels and selectors include both `app.kubernetes.io/managed-by=deploy-api` and `app.kubernetes.io/name=<name>`. Each container gets requests of CPU `100m` and memory `64Mi`, and limits of CPU `500m` and memory `128Mi`.

Bodies must be exactly one JSON object containing name, image, and integer containerPort. Unknown fields, null or array bodies, trailing values, invalid names, empty images, and ports outside 1 to 65535 return `400` before any Kubernetes requests. Existing Deployments or Services return `409`, including unmanaged resources. Neither existing resource is replaced.

Errors contain a stable code, a readable message, and resource outcomes. For example, a controlled Kubernetes denial after Deployment creation returns `503` with this shape.

```json
{"error":{"code":"upstream_unavailable","message":"services is forbidden by policy","details":{"name":"learn-a","namespace":"deploy-api","resources":{"deployment":"created","service":"failed"},"reason":"Forbidden"}}}
```

A single 10-second budget covers preflight reads and both sequential writes. The API checks both names before creating the Deployment, then creates the Service. It retains the Deployment if the Service fails. Outcomes are `created`, `failed` for a known server rejection, `unknown` for a write whose response was lost or timed out, and `not_attempted` when no write was issued. Internal errors also leave writes `unknown` because client-go can report the same error after an accepted write whose response cannot be decoded. Inspect both resources before retrying; a retry may return `409`. The API does not roll back or repair partial apps. Inspection and deletion endpoints follow in later PRs. Do not remove a colliding unmanaged resource.

Kubernetes absence returns `404`, conflicts return `409`, permission denial or unavailability returns `503`, upstream timeout returns `504`, and unexpected internal failures return `500`. Kubernetes reason/message details are preserved. Raw transport URLs and errors are not exposed.

The offline tests use fake clients for resource intent and real typed clients against controlled HTTP upstreams for sequential requests, Service rejection, disconnects, and shared deadlines.

```sh
go test ./internal/kube -run TestCreate -v
go test ./internal/httpapi -run TestCreate -v
go test . -run TestRun -v
```

## List complete and partial applications

```sh
curl --fail-with-body -i http://127.0.0.1:8080/apps
kubectl --context=docker-desktop -n deploy-api get deployments,services \
  -l 'app.kubernetes.io/managed-by=deploy-api,app.kubernetes.io/name'
```

The HTTP response returns one entry per managed application, sorted by name. Presence flags describe existing resources, including resources that are deleting. They do not indicate readiness. A retained Deployment after a failed Service creation remains discoverable.

```json
{"apps":[{"name":"learn-a","namespace":"deploy-api","resources":{"deployment":true,"service":true}},{"name":"partial","namespace":"deploy-api","resources":{"deployment":true,"service":false}}]}
```

Each resource must have `app.kubernetes.io/managed-by=deploy-api` and `app.kubernetes.io/name` equal to its resource name. A managed resource with an unmanaged counterpart reports only the managed resource's presence. Service-only apps also appear. Other namespaces and mislabeled resources do not appear. An empty inventory returns `{"apps":[]}`.

Restart the API and repeat the request to confirm discovery comes from Kubernetes rather than process memory. A single 10-second budget covers both required list calls and respects an earlier incoming deadline. The two lists are separate observations, rather than an atomic Kubernetes snapshot. If either list fails, the request returns a JSON error instead of a partial successful inventory. Read errors omit creation outcomes. Denied or unavailable reads return `503`, including a missing upstream namespace or list endpoint. Timeouts return `504`, and unexpected failures return `500`.

Run the offline listing checks without cluster access.

```sh
go test ./internal/kube -run TestList -v
go test ./internal/httpapi -run TestList -v
```

## PR-03 verification status

PR-03 implements `GET /apps`. Local verification ran on 2026-10-21 with Go `1.26.0` and Docker Desktop Kubernetes `v1.34.1`. The final uncached tests, race tests, `go vet ./...`, `go build ./...`, formatting check, and `git diff --check` passed. Default tests remain offline.

The compiled API listed two complete apps, a Deployment-only app, and a Service-only app in sorted order with exact presence flags. Unmanaged, wrong-manager, missing-name-label, and mismatched-name-label fixtures were excluded. Restarting the process preserved discovery. Twenty repeated live list requests returned the same inventory within the 11-second budget. Fixture cleanup checked each resource UID before deletion, restored the original Deployment, Service, ReplicaSet, and Pod inventory, and retained namespace `deploy-api`. These checks establish real Kubernetes discovery, rather than readiness or Service networking.

The controlled upstream exercised the compiled API through a separate test kubeconfig. It verified empty `apps: []`, both required read failures, and missing namespace or resource endpoints. Forbidden and NotFound list responses returned `503 upstream_unavailable` with their Kubernetes reason/message preserved. A rejected Service create left a Deployment discoverable after a full API process restart. This is transport and application evidence, not live controller evidence.

One hundred controlled list requests over 20 complete or partial apps measured a p95 of 201.39 ms, below the 500 ms acceptance budget. Every response matched the expected sorted union, and every read met the 11-second limit. Five alternating trunk/head batches also checked health. Trunk `0d3f38c` returned `404` for listing, and the PR-02 parent `ef04fa4` returned `405`; neither implements an equivalent list operation. No speedup comparison is claimed. The fixture used the normal client-go configuration, including its default rate limits, on a shared Mac.

The live fixtures used `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`. Registry inspection confirmed `linux/arm64` support before creation. Listing does not depend on image pulls or workload readiness.

The primary checkout retains rerunnable scripts and raw evidence in `.scratch/pr03/`. `verify_offline.py` saves commands, exit codes, and logs. `verify_live.py` saves HTTP responses, Kubernetes objects, process logs, and cleanup checks. `verify_controlled.py` saves the fixture dataset, upstream requests, per-sample timings, and failure responses. The corresponding records are `offline-results.json`, `live-results.json`, and `controlled-results.json`. These local files are excluded from Git.

Independent Codex review found that required List NotFound errors incorrectly returned `404`. Regression tests reproduced both resource types before a List-only fix. The final checks verify `503` while preserving creation behavior. Comment review found no scoped comments or suppressions. The source also updates HTTP route wiring and keeps new listing tests in separate files beyond the plan's proposed paths.

Only Codex roles ran, as requested. The plan's ten independent verification-worker lanes and cross-provider review are incomplete. Computer Use refused Terminal access, so terminal screenshots and video remain unavailable. Formal media, independent-worker, CI, and operator merge gates remain open. The parent-run evidence above does not replace those gates.

## PR-02 verification status

Local verification ran on 2026-10-21 against Docker Desktop Kubernetes `v1.34.1`, using Go `1.26.0`. The namespace was already present. Default tests remain offline.

Ten live applications returned `202` with the expected Location and response body. Their Deployments and Services matched the required labels, selectors, probe, rolling settings, pull policy, ports, and resource budgets. All ten rolled out and served nginx through their own Services. Managed duplicates returned `409` with unchanged UIDs. Both unmanaged collision shapes returned `409`, preserved the existing UID, and created no companion resource. Nineteen invalid requests returned `400` without changing workload inventory. Cleanup checked fixture UIDs before deletion, restored the original workload inventory, and retained the namespace.

The ten acceptance samples ranged from 16.19 to 56.29 ms, with a median of 25.82 ms. Service access took 4.60 to 83.38 seconds, including image pulls and rollout. Every sample met the absolute budgets of 11 seconds for acceptance and 180 seconds for access. These are local acceptance measurements on a busy shared Mac, not a speedup claim. On trunk commit `0d3f38c`, ten POST requests returned `404` and ten health requests returned `200`. Trunk has no equivalent create operation to compare.

The fixtures used these distinct image indexes after registry inspection confirmed `linux/arm64` support.

- `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`, resolved from `stable-alpine`.
- `docker.io/library/nginx@sha256:892bb2152464f0c5d8972593a5f5afaf8054552580b699d95f8ff4af08c2d33d`, resolved from `mainline-alpine`.

The local rerun scripts and raw results are in the primary checkout's `.scratch/pr02/` directory. `verify_live.py` saves HTTP transcripts, actual resources, port-forward logs, timings, and cleanup results in `live-results.json`. `verify_offline.py` records commands, exit codes, and test output in `offline-results.json`. These local artifacts are excluded from Git.

After the response-classification fix, `verify_pair.py` exercised the final binary with two different-image applications. Both Services returned HTTP `200` through simultaneous port-forwards on local ports 8081 and 8082. Both forwards remained alive during the checks. The script then removed its UID-checked fixtures and confirmed the original workload inventory. `pair-results.json` and `pair-transcript.log` retain the responses and cleanup result.

Independent native review identified response-classification gaps after accepted writes with malformed headers or truncated bodies. Focused typed-upstream tests reproduced both failures before the fix. The final `go test -count=1 -v ./...`, `go test -race -count=1 ./...`, `go vet ./...`, and `go build ./...` runs all passed after the fix. `gofmt -l` produced no filenames, and `git diff --check` passed. Controlled upstream tests cover the shared deadline, cancellation, retained Deployment, known rejection, uncertain write, and credential-safe transport errors. These tests establish HTTP and application behavior; the live exercises establish controller behavior and networking.

The prescribed cross-provider review and independent verification swarm remain incomplete. Claude failed authentication, and the Grok CLI is unavailable. Terminal screenshots and video could not be recorded because the Computer Use tool refused Terminal access. These gaps remain open for operator review before merge; this record does not claim the plan's formal clean verdict.

The creation handler requires the Kubernetes client that passed startup checks. PR-02 therefore also updates `main.go`, `main_test.go`, `internal/httpapi/server.go`, and `internal/httpapi/server_test.go` beyond the plan's proposed paths. `internal/httpapi/upstream_test.go` keeps controlled transport tests separate from request-validation tests. These wiring and test-path adjustments do not add another application endpoint.

## Check startup failures

Stop the API before each failure check. Start with a nonexistent explicit path.

```sh
go run . -kubeconfig /nonexistent/deploy-api-kubeconfig
curl --connect-timeout 1 http://127.0.0.1:8080/healthz
```

Expect a nonzero process exit and no listener. For malformed YAML, a missing `docker-desktop` context, an unreachable server, or namespace access failures, use a separate test kubeconfig file. Keep real kubeconfig credentials out of transcripts. Confirm the process exits before it serves health.

Use the controlled-upstream tests for namespace NotFound, Forbidden, shared deadline, and cancellation behavior.

```sh
go test ./internal/kube -run 'TestLoad|TestCheck' -v
go test . -run TestRunStartsOnlyAfterSuccessfulStartup -v
```

These tests use temporary kubeconfig files and real typed client-go clients against local HTTP test servers. They do not read your real kubeconfig or mutate cluster resources. The startup request allowlist is `GET /version` followed by `GET /api/v1/namespaces/deploy-api`.

## Verification status

Task 01 was verified locally on 2026-10-21 after full filesystem and network access became available. Go reports `go1.26.0 darwin/arm64`. Docker Desktop reports Kubernetes `v1.34.1` on `linux/arm64`, and namespace `deploy-api` exists. Application source did not change during verification.

The following checks passed.

- `go test -count=1 ./...` passed all packages, including controlled HTTP upstreams, startup ordering, shared request deadlines, cancellation, read-only request intent, and upstream error handling.
- `go test -race -count=1 ./...` passed all packages.
- `go vet ./...` and `go build -o /tmp/deploy-api-verify/deploy-api .` passed.
- `gofmt -l` produced no filenames for the six Go files.
- The compiled executable exited with code 1 for a missing explicit file, invalid YAML, and missing `docker-desktop` context. None of those runs logged HTTP startup.
- Real startup successfully read the Kubernetes version and namespace before serving `GET /healthz` on `127.0.0.1:8080`. The response was 200 with JSON content type and `{"status":"ok"}`. POST health returned 405 and GET apps returned 404.
- Twenty live start/stop cycles completed with successful health and clean SIGINT exits. One hundred measured health requests returned the expected response. After each shutdown, the task API no longer served health.
- Deployment, Service, ReplicaSet, and Pod inventories in `deploy-api` were unchanged before and after the live exercises. No cluster mutations were attempted.

The accepted local measurement batch had a maximum process-start-to-health time of 165.34 ms and a health p95 of 0.63 ms. Both met the plan's absolute budgets of 11 seconds and 250 ms. These figures describe one batch on a shared Mac, including process startup and sequential loopback requests. They are acceptance observations, not a comparative speedup or throughput claim. No executable trunk baseline exists because the directory had no Git repository before this work.

An initial unpaced restart batch encountered `address already in use` at iteration 10 while an unrelated Solina IPv6 listener used port 8080. The accepted batch inserted a 200 ms pause between cycles and passed all 20 cycles. Solina was left running throughout. The task API's IPv4 listener could coexist with that listener during the successful checks.

The rerunnable local verification script is `.scratch/task01-verification/verify_live.py`. It uses the binary path above, validates response bodies, checks process exit codes, and saves raw samples in `live-results.json`. Adjacent startup logs, routing results, inventory snapshots, and `initial-rapid-restart-conflict.log` retain both successful and failed observations. These local files are excluded from Git.

`git init -b main` succeeded during verification. No remote was configured and no publication occurred. The plan's formal PR, independent-verifier, screenshot/video, and operator review gates remain open because no PR was requested or created.
