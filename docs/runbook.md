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

Acceptance means both resource writes succeeded. Kubernetes creates ReplicaSets and Pods afterward. The response does not wait for readiness. The status URL returns configuration and diagnostics through `GET /apps/{name}`. Application `ready` remains conservatively false until PR-06 adds the rollout readiness predicate.

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

A single 10-second budget covers preflight reads and both sequential writes. The API checks both names before creating the Deployment, then creates the Service. It retains the Deployment if the Service fails. Outcomes are `created`, `failed` for a known server rejection, `unknown` for a write whose response was lost or timed out, and `not_attempted` when no write was issued. Internal errors also leave writes `unknown` because client-go can report the same error after an accepted write whose response cannot be decoded. Inspect both resources before retrying; a retry may return `409`. The API does not roll back or repair partial apps. The inspection endpoint follows in a later PR; deletion is described below. Do not remove a colliding unmanaged resource.

Kubernetes absence returns `404`, conflicts return `409`, permission denial or unavailability returns `503`, upstream timeout returns `504`, and unexpected internal failures return `500`. Kubernetes reason/message details are preserved. Raw transport URLs and errors are not exposed.

The offline tests use fake clients for resource intent and real typed clients against controlled HTTP upstreams for sequential requests, Service rejection, disconnects, and shared deadlines.

```sh
go test ./internal/kube -run TestCreate -v
go test ./internal/httpapi -run TestCreate -v
go test . -run TestRun -v
```

## Delete complete and partial applications

```sh
curl --fail-with-body -i -X DELETE http://127.0.0.1:8080/apps/learn-a
kubectl --context=docker-desktop -n deploy-api get deployment,service,replicaset,pod \
  -l 'app.kubernetes.io/managed-by=deploy-api,app.kubernetes.io/name=learn-a'
kubectl --context=docker-desktop get namespace deploy-api
```

A valid name follows the same rules as creation. Deletion reads the Deployment and Service independently and checks both ownership labels, namespace, UID, and resourceVersion. Each DELETE carries the observed UID and resourceVersion as preconditions. A replacement object or an ownership change between the read and write returns `409` without deleting that changed resource. Missing identity metadata fails closed. A colliding unmanaged resource returns `409`; deletion still attempts the other managed resource.

The response is `202` with `Location: /apps/learn-a` when either resource has an accepted deletion request or is already deleting. For example, deleting a Deployment-only app returns this JSON.

```json
{"name":"learn-a","namespace":"deploy-api","resources":{"deployment":"delete_requested","service":"absent"},"statusUrl":"/apps/learn-a"}
```

Deletion outcomes are `delete_requested`, `deleting`, and `absent`. A rejected write is `failed`; an uncertain write is `unknown`; a failed lookup or a resource rejected before writing is `not_attempted`. A missing resource, including a resource that disappears between GET and DELETE, is `absent`. Both resources absent returns a bodyless `204`, including repeated deletion after cleanup. The status URL reserves the future inspection endpoint. Use kubectl to inspect resources until that endpoint is implemented.

A single 10-second budget covers both resource helpers. The Service helper runs after a Deployment lookup or write failure, subject to that same deadline. An error on either resource prevents `202`. Error details retain both outcomes and each resource's normalized code, message, and reason. For example, a Deployment conflict followed by a Service permission denial returns `503` with this shape.

```json
{"error":{"code":"upstream_unavailable","message":"services is forbidden by policy","details":{"failures":{"deployment":{"code":"conflict","message":"Deployment precondition conflict","reason":"Conflict"},"service":{"code":"upstream_unavailable","message":"services is forbidden by policy","reason":"Forbidden"}},"name":"learn-a","namespace":"deploy-api","resources":{"deployment":"failed","service":"failed"},"reason":"Forbidden"}}}
```

For mixed errors, timeout (`504`) takes precedence over unavailable or denied access (`503`), then internal errors (`500`), then conflicts (`409`). Equal-priority errors use the Deployment failure for the top-level code and message. Both resource failures remain in `details.failures`. Raw transport errors and URLs are sanitized. A timeout, disconnect, or undecodable DELETE response can follow an accepted deletion and is reported as `unknown`; inspect before retrying. A failed GET never implies a deletion was attempted.

Deployment deletion explicitly requests background propagation. Kubernetes garbage collection removes owned ReplicaSets and Pods asynchronously. The API does not delete children or the namespace, and it does not wait for cleanup. A `202` response does not prove that dependent resources have disappeared.

```sh
go test ./internal/kube -run TestDelete -v
go test ./internal/httpapi -run TestDelete -v
```

The default offline tests use fake clients for ownership and partial-operation branching. Controlled HTTP upstreams exercise the real typed client, validate UID and resourceVersion delete preconditions and background propagation, reject simulated concurrent replacements and ownership changes, and cover rejection, ambiguous responses, cancellation, and a shared deadline. These tests do not prove live garbage collection, survivor availability, or cleanup timing. Those require the explicit cluster checks in the implementation plan.

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

## Inspect an application

Read the desired configuration and current Kubernetes diagnostics.

```sh
curl --fail-with-body http://127.0.0.1:8080/apps/learn-a
kubectl --context=docker-desktop -n deploy-api get deployment,service learn-a -o yaml
kubectl --context=docker-desktop -n deploy-api get replicasets,pods -l app.kubernetes.io/name=learn-a -o yaml
```

The JSON contains `name`, `namespace`, `resources`, `ready`, and `pods`. `resources` reports Deployment and Service presence independently. Deployment-only and Service-only apps return `200`. A Service-only app omits `image`, `containerPort`, and `deployment` because its requested container configuration is unknown. An absent app returns `404`. An unmanaged resource with that name returns `409`.

When a Deployment exists, `image` and `containerPort` come from its named `app` container. `deployment` includes generation, observed generation, desired and total replica counts, updated, ready, and available replica counts, and raw conditions. Both resource diagnostics expose deletion timestamps when set.

Pods are sorted by name and must match both app labels and the controller UID chain from the current Deployment through a ReplicaSet. Matching labels alone do not establish membership. Terminating owned Pods remain visible with their deletion timestamps.

Each Pod includes its phase and Pod readiness. Its optional `container` object projects the named `app` container status. This includes the reported image, image ID, readiness, restart count, current `state`, and `lastTerminationState`. Waiting and terminated states preserve Kubernetes reasons and messages. Previous termination includes the exit code, including zero. A Pod without container status omits `container`; an unreported state is an empty object.

Application `ready` requires both resources present and not deleting. The Deployment controller must have observed the current generation. Desired, total, updated, ready, and available replica counts must each equal one. A non-deleting owned Pod must belong to a non-deleting ReplicaSet with the current Deployment template. That Pod and its running `app` container must be ready, and its spec image must match the requested image.

Template comparison ignores Kubernetes' `pod-template-hash` label. Other template changes, including annotations with the same image, require a matching ReplicaSet. Runtime-reported image strings remain diagnostic data because the runtime can normalize them. An old healthy Pod cannot satisfy a failed requested rollout. Raw conditions such as `ProgressDeadlineExceeded` remain visible, and do not prevent later recovery.

Inspection does not wait for rollout. Its four required Kubernetes reads and the best-effort warning-event read share one 10-second operation deadline. Required read failures return errors. An events-only failure preserves the core response as described below.

Compare readiness with Kubernetes after creating an app with a verified image and matching port.

```sh
kubectl --context=docker-desktop -n deploy-api rollout status deployment/learn-a --timeout=180s
curl --fail-with-body http://127.0.0.1:8080/apps/learn-a
kubectl --context=docker-desktop -n deploy-api get deployment,service learn-a -o yaml
kubectl --context=docker-desktop -n deploy-api get replicasets,pods -l app.kubernetes.io/name=learn-a -o yaml
```

Expect `ready=true` after the matching complete rollout. During startup, a wrong-port probe failure, missing resources, deletion, or an unobserved generation, expect `ready=false`. For a failed replacement exercise before the image-update endpoint exists, change the Deployment's `app` image with `kubectl set image` to an intentionally missing tag. Inspect both the old healthy Pod and the new pull failure. Readiness must remain false, including after `ProgressDeadlineExceeded`. Restore the original verified image and compare the recovered rollout. Save HTTP responses and matching Kubernetes objects for actual cluster evidence.

```sh
go test ./internal/kube -run 'TestRolloutReady|TestInspect' -count=1
go test ./internal/httpapi -run TestInspect -count=1
```

These offline tests use snapshots, fake clients, and controlled HTTP upstreams. They establish readiness decisions and HTTP behavior. They do not verify image pulls, probes, scheduling, real rollout convergence, or inspection latency against a live cluster. The commands above describe manual checks and do not claim that PR-06 live or performance verification has run.

## PR-04 local verification

Local verification passed on 2026-10-22 against Docker Desktop Kubernetes v1.34.1. `go test -count=1 -v ./...`, `go test -race -count=1 ./...`, `go vet ./...`, and `go build ./...` passed. `gofmt -l` returned no filenames and `git diff --check` passed. The independent native code review and comment audit found no source changes needed.

The compiled API deleted ten isolated fixtures, including Deployment-only and Service-only applications, plus a surviving application after its HTTP availability was checked through a port-forward. Each accepted deletion converged to no Deployment, Service, ReplicaSet, or Pod for that app, and each repeat returned a bodyless `204`. A finalizer-held Service returned `202` with `deleting` on a repeat, then `204` after the fixture finalizer was removed. An unmanaged Service returned `409` and retained its UID. Namespace `deploy-api` remained, and the original workload inventory was restored. The API and all test port-forwards stopped cleanly.

The fixture image was `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`, inspected for `linux/arm64` support before the run. Across eleven measured accepted deletions, the largest HTTP acceptance time was 77.80 ms and the largest acceptance-to-observed-cleanup time was 4.64 seconds. These are one local batch's absolute acceptance checks, below 11 and 180 seconds respectively. Trunk and PR-03 rejected DELETE for owned fixtures, so no equivalent deletion latency baseline or speedup is claimed. Compilation and image preparation were outside the timed DELETE requests. Registry, controller, scheduling, and polling costs affect these observations.

Rerunnable local scripts, raw HTTP and kubectl transcripts, samples, binary and source hashes, and offline results are in the primary checkout's `.scratch/pr04/` directory. `verify_live.py` checks isolated fixture identity before cleanup and records `live-results.json`; `verify_offline.py` records `offline-results.json`. Go sources were unchanged between offline checks, binary compilation, and the live run.

Formal cross-provider design and review coverage, the prescribed independent verification swarm, terminal screenshots, video, and operator review remain open. Claude failed authentication, Grok is not installed, and Computer Use refused Terminal access. Local verification does not claim the plan's formal clean verdict or authorize merging.

The HTTP route adds `internal/httpapi/server.go` beyond PR-04's proposed file list. Dedicated `internal/kube/delete_test.go`, `internal/httpapi/delete_test.go`, and `internal/httpapi/delete_upstream_test.go` keep deletion checks separate. Existing creation test files migrate to the shared `ResourceOutcome` and `ResourceOutcomes` names and add the required test-service method. Creation JSON remains unchanged.

## PR-05 local verification

Local checks passed on 2026-10-22 with Go `1.26.0` and Docker Desktop Kubernetes `v1.34.1`. `go test -count=1 -v ./...`, `go test -race -count=1 ./...`, `go vet ./...`, and `go build ./...` passed. `gofmt -l` returned no filenames, and `git diff --check` passed. Independent native review found no code defects. Its comment audit found no added source comments or suppressions.

Offline tests cover complete and partial apps, absence, unmanaged collisions, Deployment conditions and counts, deleting resources, missing status, waiting, running, terminated, and previous termination states. Ownership tests exclude Pods with matching labels but no controller, wrong labels, old Deployment or ReplicaSet UIDs, noncontroller references, and wrong owner names or kinds. Typed HTTP upstream tests exercise all four required reads, error mapping, credential-safe transport errors, cancellation at every read, and the shared ordinary deadline.

The compiled API created three isolated live fixtures using the inspected arm64 image `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`. Complete, Deployment-only, and Service-only inspections returned `200`. An absent app returned `404`. Deployment configuration, generation, conditions, replica counts, Pod phase and readiness, and container state matched captured Kubernetes objects. A separate Pod with matching app labels and no ownership chain was excluded. All three app shapes remained inspectable after restarting the API. Fixture cleanup checked resource UIDs, preserved the original workload inventory, retained the namespace, and stopped the API.

The latency check used five alternating batches of 20 requests per revision against the same controlled upstream dataset of 20 ReplicaSets and 20 Pods. All 100 head inspections returned 20 owned Pods and used four upstream reads. Their p95 was 402.43 ms, below the proposed 500 ms absolute budget. All 100 health requests per revision succeeded. Parent commit `45e1531` returned `405` for all inspection requests because it lacks that endpoint. This is an acceptance observation with the default client-go transport and rate limits, not a speedup comparison. Twenty additional real-cluster inspection samples succeeded within the 11-second operation budget. The largest of all 22 complete-app reads, including comparison and restart checks, took 413.87 ms on this shared Mac.

Rerunnable scripts and raw evidence live in the primary checkout's ignored `.scratch/pr05/` directory. `verify_live.py` records HTTP responses, kubectl objects, cleanup, and timings in `live-results.json`. `verify_controlled.py` records its dataset, upstream requests, baseline responses, and samples in `controlled-results.json`. Offline command logs, race output, and source hashes are retained beside them.

The default Grok writer did not start because its CLI is unavailable. The parent implemented this slice with independent native design and code review. Cross-provider design and adversarial review, the prescribed ten-worker swarm, trunk performance baseline, screenshots, video, CI, and operator merge review remain open. Local checks do not claim the plan's formal clean verdict. This delivery stages, commits, and pushes the feature branch without merging.

The route also changes `internal/httpapi/server.go`, and `internal/httpapi/delete_test.go` gains the inspection method on its existing test service. Dedicated HTTP inspection and typed-upstream test files keep the new checks separate from creation tests. These are file-boundary adjustments to PR-05's proposed paths.

## PR-06 local verification

Local checks passed on 2026-10-22 with Go `1.26.0` and Docker Desktop Kubernetes `v1.34.1`. `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed. `gofmt -l` returned no filenames, and `git diff --check` passed. Independent native code review found no defects. The comment audit found no added source comments or suppressions.

Offline tests cover complete rollouts, stale generations, replica counts, missing and deleting resources, Pod and container readiness, old templates with the same image, ownership UID replacement between inspections, runtime image normalization, and raw progress-deadline conditions through recovery. HTTP tests check the public readiness response and unchanged Pod JSON fields. Ten additional scenarios drove the compiled API against a controlled Kubernetes HTTP upstream. They confirmed the readiness decisions and four reads per inspection. These controlled scenarios are transport and application evidence, not live Kubernetes evidence.

The compiled PR-05 and PR-06 APIs inspected the same healthy Docker Desktop fixture. PR-05 returned `ready=false`; PR-06 returned `ready=true` with one desired, total, updated, ready, and available replica. Twenty additional PR-06 inspections returned ready. A fresh PR-06 rollout became ready within five seconds after a matching complete Deployment snapshot was observed. A wrong-port fixture remained unready while its container ran and after the controller reported raw `ProgressDeadlineExceeded`. That fixture used a 20-second progress deadline to bound the exercise. No rollback occurred. Deployment-only and Service-only fixtures remained unready. Cleanup checked fixture UIDs, restored the original workload inventory, and retained the namespace.

Before the live run, `docker buildx imagetools inspect` confirmed `linux/arm64` support for `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`. The first attempt detected an existing repository API on port 8080 and is excluded from verification. Its fixtures were removed after UID checks. The existing API was temporarily stopped for serial baseline and head checks, then restored.

The comparative latency check used the PR-05 controlled dataset of 20 ReplicaSets and 20 Pods. Five alternating batches produced 100 successful inspections per revision, with no failures and four upstream reads per inspection. Inspection p95 was 404.04 ms on PR-05 and 404.62 ms on PR-06, below the proposed comparative budget of 484.85 ms. Both sides used the shipped client-go configuration on a shared eight-core Mac. Low sampled process CPU and the paced request pattern are consistent with client-go rate limiting, but no profile established a definitive limiter. This is an acceptance-budget check, not a speedup claim. The PR-05 baseline source commit `30aeab0` has the same tree as PR-06's merge-base `7724968`.

Rerunnable scripts, raw HTTP and kubectl transcripts, datasets, per-request timings, source hashes, offline logs, and provider dropout receipts are retained in the primary checkout's ignored `.scratch/pr06/` directory. `verify_live.py`, `verify_scenarios.py`, and `verify_controlled.py` reproduce the local checks. The throughput checkpoint keeps one implementation writer in its own worktree, independent verification artifacts, and serialized real API processes.

Claude could not authenticate and Grok was not installed. Cross-provider design and review, the prescribed independent swarm, screenshots, video, CI, and operator merge review remain open. Native review and local checks do not claim the plan's formal clean verdict or authorize merging. `internal/kube/ownership.go` extends PR-06's proposed paths to retain ReplicaSet template evidence without another read. `internal/httpapi/inspect_test.go` adds observable HTTP coverage.


## Inspect warning events and failure causes

`GET /apps/{name}` includes `warningEvents`, an array of at most ten Warning events for the current Deployment, its owned ReplicaSets, and its owned Pods. Each entry preserves `involvedObject`, plus `reason`, `message`, and `timestamp` when available. Membership uses object UIDs from the same inspection ownership graph. A matching app name or label alone does not qualify an event. Service events are outside this graph. Service-only apps return an empty array without an event lookup.

The API configures one shared Kubernetes client limiter at 20 requests per second with a burst of 30. This bounds traffic across API groups while allowing the five-read inspection path to meet the local latency budget.

Warnings sort newest first. Their occurrence time uses the first available value from series last-observed time, last timestamp, event time, first timestamp, and creation timestamp. Events without a timestamp sort last. Equal or absent timestamps sort by event name and then UID, ascending. An empty array does not imply success because Kubernetes events expire.

If only the event lookup fails, inspection returns `200` with the completed core diagnostics, `diagnosticsIncomplete=true`, and `diagnosticError` in the existing structured error shape. Transport errors omit connection details and credentials. A successful lookup returns `diagnosticsIncomplete=false` and omits `diagnosticError`. Event lookup shares the remaining operation budget. Its timeout does not discard completed readiness or Pod diagnostics. Failures in required resource reads still return HTTP errors.

Use isolated names for these exercises. Start the API, and confirm no existing app uses a fixture name before creating it. The commands below use the default API address. The local PR-07 verification used port 18080 through a build overlay because an unrelated application occupied port 8080.

### Observe an image-pull failure

```sh
curl --fail-with-body -H 'Content-Type: application/json' \
  -d '{"name":"pull-demo","image":"docker.io/library/nginx:deploy-api-intentionally-missing-20261021","containerPort":80}' \
  http://127.0.0.1:8080/apps
curl --fail-with-body http://127.0.0.1:8080/apps/pull-demo
kubectl --context=docker-desktop -n deploy-api describe pods -l app.kubernetes.io/name=pull-demo
```

Poll inspection and compare actual waiting reasons and events with Kubernetes. Expect `ready=false`. `ErrImagePull` or `ImagePullBackOff` describes a pull failure. Registry access errors can also prevent the pull, so retain the actual message.

### Observe a failed TCP readiness probe

Resolve and verify the nginx image as described in the specification. Set `IMAGE_A` to its immutable index digest.

```sh
curl --fail-with-body -H 'Content-Type: application/json' \
  -d "{\"name\":\"probe-demo\",\"image\":\"$IMAGE_A\",\"containerPort\":81}" \
  http://127.0.0.1:8080/apps
curl --fail-with-body http://127.0.0.1:8080/apps/probe-demo
kubectl --context=docker-desktop -n deploy-api describe pods -l app.kubernetes.io/name=probe-demo
```

The nginx process can run while its configured TCP probe fails on port 81. Compare `ready=false`, the running container state, and `Unhealthy` warnings. This is different from a container that cannot pull or exits after startup. The Deployment can later expose `ProgressDeadlineExceeded`. The API preserves that condition without declaring a permanent application failure.

### Observe a process that exits and restarts

Follow all five steps of the [crashing-container exercise](spec.md#reproducible-crashing-container-exercise). Resolve `docker.io/library/postgres:17.6-alpine`, verify arm64 support, and record its index digest before creating `crash-demo` on port 5432. Leave the image's default entrypoint and environment unchanged. After a successful pull, allow up to 180 seconds for repeated exits and backoff.

Compare `ready=false`, an increasing `restartCount`, previous termination exit code `1`, and eventual `CrashLoopBackOff` with `kubectl describe pod`. The phase may still be `Running`, and waiting reasons can change between polls. Use `kubectl logs <pod> -c app --previous` to confirm the missing-password startup error. Previous-instance logs remain a kubectl verification step. No previous-log API option is added.

Compare warning `involvedObject.uid` values with the captured Deployment, ReplicaSet, and Pod UIDs. Delete each fixture through `DELETE /apps/{name}` after the comparison. Confirm its owned children disappear and namespace `deploy-api` remains.

```sh
go test ./internal/kube -run 'TestWarning|TestInspect' -count=1
go test ./internal/httpapi -run TestInspect -count=1
```

These tests run offline with fake clients and controlled HTTP upstreams. They cover Warning and UID filtering, timestamp fallback, deterministic ordering, the ten-event limit, preserved crash state, event-only denial and timeout, transport-error sanitization, and the shared deadline.


## PR-07 local verification

Local offline checks passed on 2026-10-22 with Go `1.26.0`. `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed. `gofmt -l` returned no filenames and `git diff --check` passed. Two independent native source reviews found no correctness blockers. The scoped comment audits found no comments or suppressions to remove.

The Docker Desktop Kubernetes `v1.34.1` exercises distinguished three failures. An intentionally missing nginx tag produced image-pull waiting reasons. A running nginx container on the wrong probe port remained unready and exposed `Unhealthy` warnings and raw `ProgressDeadlineExceeded`. The postgres fixture exited with code `1`, increased its restart count, and entered `CrashLoopBackOff`. Its previous-instance logs named the missing `POSTGRES_PASSWORD`. Warning object UIDs, reasons, and messages matched captured Kubernetes objects and events. PR-06 inspection preserved the same core pull-failure diagnosis and lacked the new event fields. Cleanup checked fixture resource UIDs, removed their owned children, restored the original workload inventory, and retained namespace `deploy-api`.

The inspected arm64 nginx index was `docker.io/library/nginx@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5`. The inspected postgres `17.6-alpine` index was `docker.io/library/postgres@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94`. Image preparation and registry pull time were outside the inspection latency comparison. The crash observation bound began after a successful process exit had established that the image could run.

The initial repeated-inspection comparison missed the proposed budget. PR-06 p95 was 404.85 ms and the untuned PR-07 p95 was 610.03 ms against a 504.85 ms limit. Recorded upstream requests showed approximately 200 ms gaps for core-group calls, consistent with the installed client-go default five-request-per-second token interval. PR-07 now explicitly configures a shared clientset limiter at 20 requests per second with a burst of 30. This changes rate limiting for all Kubernetes calls, including startup and writes. It permits more upstream traffic and changes contention across API groups. The chosen values are a bounded local configuration, not an optimal cluster-sizing result.

The final comparison used each revision's shipped client configuration against the same controlled dataset of 20 Pods and 20 warnings. Five alternating batches produced 100 successful inspections per revision and 100 successful health requests per revision. PR-06 inspection p95 was 408.56 ms and PR-07 p95 was 255.26 ms, below the proposed 508.56 ms comparative limit. Per-batch p95 ranged from 401.42 to 419.28 ms on PR-06 and 253.45 to 256.60 ms on PR-07. Each baseline inspection made four upstream reads. Each head inspection made five and returned the newest ten warnings. The machine had eight cores and other active applications. This checks the end-to-end acceptance budget with different limiter configurations. It does not establish an algorithmic speedup.

A controlled upstream that stalled only events returned HTTP `200` with all 20 core Pods, `diagnosticsIncomplete=true`, and an upstream-timeout diagnostic after 10.01 seconds. This was within the overall ten-second deadline plus the one-second tolerance. Short-deadline HTTP tests also checked identical deadlines on every read and upstream cancellation. These are transport and application checks, distinct from real Kubernetes controller evidence.

Rerunnable scripts, source hashes, image inspections, command logs, raw HTTP and kubectl objects, per-request timings, and provider dropout receipts are retained in the primary checkout's ignored `.scratch/pr07/` directory. `verify_live.py`, `verify_controlled.py`, and `verify_timeout.py` produce their corresponding result JSON files. Initial untuned results remain under `untuned-*`. Build overlays change only the verification listener address to ports 18080, 18081, or 18082. Production `main.go` retains port 8080. The unrelated process on that port was preserved. No worktree was created.

Claude was unauthenticated and the Grok CLI was unavailable. The parent implemented the change with independent native design and source review. Cross-provider review, the prescribed independent verification swarm, terminal screenshots, video, CI, and operator merge review remain open. Local checks do not claim the plan's formal clean verdict or authorize merging. The implementation adds `internal/kube/client.go` to PR-07's proposed file scope for the measured rate-limit adjustment. HTTP transport tests and the implementation checklist also change to cover the fifth read, shared-deadline degradation, and the recorded implementation status.

## Follow one Pod's logs

Select a Pod from `GET /apps/{name}` and pass its name to the logs endpoint.

```sh
APP=your-app
POD=your-pod
curl -N --max-time 20 "http://127.0.0.1:8080/apps/$APP/logs?pod=$POD&tailLines=100"
kubectl --context=docker-desktop -n deploy-api logs "$POD" -c app --tail=100
```

The endpoint follows only the selected Pod's `app` container. It verifies the managed Deployment and the ReplicaSet and Pod controller UIDs before requesting logs. An unrelated Pod returns `409`. A missing Deployment or Pod returns `404`. A container that has not started can return `409` with the Kubernetes message.

`pod` is required. `tailLines` defaults to 100 and accepts integers from 0 through 1000. Invalid names, malformed queries, duplicate selection parameters, and invalid tail counts return `400` JSON. Initial Kubernetes permission failures return `503`, and setup timeouts return `504`, before success headers.

Ownership reads and upstream establishment share a ten-second budget. Once established, the body has no total operation deadline. The API flushes headers and each chunk, including partial lines. Disconnect cancels the upstream request and closes its body. EOF ends the response. A failure after success headers closes the response and appears in the API process log. Reconnect explicitly after a container restart or Pod replacement. The API does not switch Pods or request previous-instance logs.

## PR-08 local verification

Offline checks passed on 2026-10-22. `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed. Controlled HTTP upstream tests cover UID ownership rejection before log access, default and boundary tail values, pre-header JSON errors, header and chunk flushing, EOF, truncated-body process logs, a stalled response header, a stalled error body, setup budget consumed by ownership reads, established streams surviving an injected setup timeout, and upstream cancellation on disconnect and body close. The default suite reads no real kubeconfig.

Five controlled delivery runs each checked 20 timestamped lines and one disconnect. Every delivery and cancellation met the one-second acceptance budget. These measurements check local delivery behavior, not a speedup over trunk, which has no logs endpoint.

The Docker Desktop Kubernetes `v1.34.1` exercise created two isolated nginx apps. A 20-second curl stream delivered a request log near the start and another after 15 seconds, matching `kubectl logs` for the selected Pod. `tailLines` 0 excluded earlier markers; 100 and 1000 included them. Missing selection returned `400`, an absent Pod returned `404`, and the second app's real Pod returned `409`. Trunk returned `404` for the logs route. Both revisions returned successful health responses. Fixture cleanup checked Deployment and Service UIDs, removed the owned children, preserved existing Deployments and Services, and retained the namespace.

The inspected nginx stable-alpine index was `docker.io/library/nginx@sha256:47928bf67eb62a1863cb08d9ecb52d3e704304ba614eaee17884204677702951` with a verified linux/arm64 manifest. Image pulls and rollout preparation were outside stream measurements. Existing processes occupied port 8080, so build overlays changed only the verification listener to port 18080. Production configuration retains port 8080. Initial driver output used a nonexistent nginx path and received `404`; the corrected request used a query marker on `/`, and the complete exercise passed.

Rerunnable live driver, raw command and HTTP evidence, image inspection, baseline source, timing samples, and review reports are retained in the primary checkout's ignored `.scratch/pr08/` directory. `verify_live.py` drives the built baseline and head binaries. `go test ./internal/httpapi -run TestLogsHTTPDeliveryAndCancellationBudget -count=5 -v` reruns the controlled delivery check.

Native design and source review found no blocking correctness issues. Model the Domain kept stream cancellation and body ownership together in the returned closer. Prove It Works required both controlled transport checks and actual Kubernetes output beyond 15 seconds. Grok was unavailable and Claude was unauthenticated, with receipts retained locally. Cross-provider review, the prescribed independent verification swarm, terminal screenshots, video, CI, and operator merge review remain open. Local evidence does not claim the plan's formal clean verdict. `internal/httpapi/apps.go` joins the proposed PR-08 file scope to add the log capability to the service interface; the implementation checklist records only completed local build and unit work.

## Update an application's image

Resolve a second public image digest with the same listening port before updating. Confirm `linux/arm64` support with `docker buildx imagetools inspect`. Use a different reference to request a rollout.

```sh
APP=your-app
IMAGE_B=your-verified-image-reference
kubectl --context=docker-desktop -n deploy-api get service "$APP" -o yaml
curl --fail-with-body -i -X PATCH -H 'Content-Type: application/json' \
  -d "{\"image\":\"$IMAGE_B\"}" "http://127.0.0.1:8080/apps/$APP/image"
curl --fail-with-body "http://127.0.0.1:8080/apps/$APP"
kubectl --context=docker-desktop -n deploy-api rollout status "deployment/$APP" --timeout=180s
kubectl --context=docker-desktop -n deploy-api get deployment,service,replicasets,pods \
  -l "app.kubernetes.io/name=$APP" -o yaml
```

PATCH accepts one JSON object containing only a nonempty `image`. It returns `202`, `name`, `statusUrl`, and `Location` after Kubernetes accepts the Deployment change. It does not wait for readiness. An identical reference returns the same accepted response without a Kubernetes write or a generation increment.

The operation changes the container named `app` in the fetched Deployment and preserves its other configuration. It never writes the Service. Kubernetes checks the Deployment resource version and rejects an intervening change with `409`; inspect the app before retrying. Both resources must be managed by this API, present, and not deleting. A partial, unmanaged, or deleting app returns `409`. Two absent resources return `404`. Reads and the update share the ordinary ten-second operation budget. An upstream timeout can leave the write outcome unknown; inspect before retrying.

Inspect the requested generation after a failed update. An old Pod can still serve through the stable Service while `ready=false` describes the new rollout. Use a valid image reference to recover. The API does not roll back automatically.

## PR-09 acceptance evidence map

Each row separates offline HTTP or application evidence from Kubernetes controller evidence. The named tests run with `go test ./...` and do not read real kubeconfig. Local live evidence is retained under the primary checkout's ignored `.scratch/pr09/` directory. Formal independent verification and media review remain separate gates.

| Spec criterion | Offline evidence | Real Kubernetes evidence |
| --- | --- | --- |
| 1. Two named apps and resource intent | `TestCreateResourceIntent`, `TestCreateSuccessAndDuplicate` | `verify_live.py` creates two immutable-digest nginx apps and waits for each requested rollout. Captured Deployment, Service, ReplicaSet, and Pod objects identify labels and controller ownership. |
| 2. Access through Services | Resource-intent tests verify Service target ports and the TCP probe. | Separate Service port-forwards return successful HTTP responses from each app. |
| 3. Readiness and failure diagnosis | `TestInspectRequestedRolloutReadiness`, `TestInspectContainerStates` | The driver records pull-failure waiting reasons and a running wrong-port container with `Unhealthy` warnings. It compares API output with kubectl and observes `ProgressDeadlineExceeded` for the wrong-port rollout. |
| 4. Image updates and recovery | `TestImageUpdateChangesOnlyNamedContainerImage`, `TestImageUpdateIdenticalReferenceDoesNotWrite`, `TestImageUpdateOldHealthyAndNewUnready`, `TestInspectFailedReplacementAndRecovery` | Ten healthy updates preserve Service identity. A missing-image update remains unready while the old app serves. A valid image update recovers. |
| 5. Selected Pod logs and disconnect | The accumulated logs HTTP and controlled-upstream tests cover selection, transport lifetime, and cancellation. | A 20-second selected-Pod stream delivers access markers before and after 15 seconds, matching kubectl logs. |
| 6. Partial creation, invalid requests, and isolation | `TestPartialCreateListInspectAndDeleteThroughHTTP` follows a controlled Service-creation failure through list, inspect, update rejection, and deletion. `TestCreateValidation` and `TestCreateCollisionDoesNotWrite` cover invalid and duplicate requests. | Duplicate creation returns `409`. Deleting one app leaves the second app ready and accessible. Service-creation failure remains controlled-client evidence. |
| 7. Restart discovery and upstream errors | Startup, list, inspect, and transport-error tests cover unavailable or denied upstreams. | Both real apps remain discoverable and inspectable after the API process restarts. |
| 8. Delete and cleanup | `TestDeletePresenceAndOwnership`, `TestDeleteRacePreconditionsPreserveChangedResource` | The driver checks fixture UIDs before deletion, waits for owned resources to disappear, repeats deletion, and checks the namespace and pre-existing apps. |
| 9. Crashing process | `TestInspectContainerStates` preserves restart counts and last termination state. | The verified postgres fixture exits with code `1`, restarts, and enters `CrashLoopBackOff`. Previous-instance logs identify missing `POSTGRES_PASSWORD`. |
| 10. Container budgets and checks | `TestCreateResourceIntent` and image-only mutation tests assert retained configuration. | Captured specs compare creation and replacement configuration across every healthy update. Offline tests, race detection, vet, and build provide separate application checks. |


## PR-09 local verification

Offline checks passed on 2026-10-23. `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed. Go formatting and `git diff --check` were clean. The default suite reads no real kubeconfig. Controlled HTTP tests cover stale resource versions, Deployment removal between the read and update, retained fields, identical-reference no-writes, per-stage error mapping, shared deadlines, and cancellation. A Deployment that disappears during the update returns `409`, because the previously observed Service can leave a partial app.

Two Docker Desktop Kubernetes `v1.34.1` runs each completed ten alternating healthy-image updates and the image-only preservation checks. The initial run completed 105 checks and retained all pre-existing app UIDs. After the deletion-race correction, the final build completed 104 checks through fixture deletion, including the additional live budget and stalled-rollout checks. The final global inventory-preservation assertion failed because pre-existing PR07 resources disappeared during the run; a PR08 Service also disappeared between subsequent snapshots. The captured driver issues app mutations only for its isolated PR09 fixture names, and its kubectl command log contains no mutations. Concurrent outside activity is inferred, not established by an API audit. The final global inventory-preservation result is therefore inconclusive. Fixture-to-fixture isolation passed. A separate final snapshot confirms all PR09 resources are gone and the namespace remains. The original failure transcript is retained.

The final ten healthy updates accepted in at most 174.07 ms and reached requested-rollout readiness in at most 21.30 seconds. Both met the proposed absolute budgets. These are local end-to-end acceptance measurements, not a speedup claim. Trunk returns `404` for PATCH, so its response latency is not compared with a successful update. The driver samples current Deployment status and HTTP through a Service port-forward once per polling cycle. It recorded 55 successful HTTP samples and ten unavailable-forward samples as selected Pods changed. A broken forward does not establish Service downtime, and these samples do not promise uninterrupted availability. Compilation, manifest inspection, and initial image setup are outside the update samples. The eight-core host had unrelated active workloads.

The final build exposed a new pull failure with `ready=false` while the surviving old Pod remained accessible, then recovered with a valid image. A running wrong-port fixture exposed `Unhealthy` warnings and `ProgressDeadlineExceeded`. The postgres fixture exposed exit code `1`, restarts, and `CrashLoopBackOff`, with the expected missing-password message in previous-instance logs. A 20-second selected-Pod log stream delivered both early and late access markers, matching kubectl. API restart discovery, repeated deletion, and isolation between the two PR09 apps passed.

All successful fixtures used inspected immutable image-index references with linux/arm64 manifests:

- Stable nginx used `docker.io/library/nginx@sha256:47928bf67eb62a1863cb08d9ecb52d3e704304ba614eaee17884204677702951`.
- Mainline nginx used `docker.io/library/nginx@sha256:a3554c0d0b48177b1413c26e01f695334428020d9df01243fbae3e11cfd17417`.
- The crashing postgres fixture used `docker.io/library/postgres@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94`.

The primary checkout's ignored `.scratch/pr09/` directory retains the rerunnable `verify_live.py`, raw command and HTTP logs, initial and final results, image inspections, source hashes, gate exit-code receipts, and native review reports. Build overlays changed only the verification listener to port 18080. Production still binds port 8080. The first controlled transport run failed because its JSON mock did not request JSON from client-go; the fixture was corrected and aggregate tests passed. That failed output remains recorded separately.

Native source review found and corrected the deletion race, and final review found no remaining source blockers. Deslop and comment audits found no flags. Grok was unavailable and Claude was unauthenticated; their receipts are retained. Cross-provider review, the prescribed independent swarm, terminal screenshots, video, CI, and operator merge review remain open. The shared-cluster inventory check also needs a quiet-cluster rerun before claiming the plan's formal clean verdict. This PR does not authorize merging.
