# Local Kubernetes Deployment API

Status: task 01 is implemented and locally verified. Later tasks have not started. The subject of review point 1 remains unspecified; no additional amendment has been inferred.

## Purpose and success

Build a small Go HTTP API to learn how an existing container image becomes a running application in Kubernetes. The developer already knows Go, Docker, and backend deployment; the learning focus is Kubernetes resources, asynchronous convergence, diagnosis, and direct API access. AI-generated implementation must remain understandable and independently verifiable with kubectl.

Success means completing and explaining the lifecycle of multiple independently named applications: deploy, access, inspect, stream logs, update, and delete. A future Northflank-like platform is motivation, not this project's scope.

## Environment and technical choices

- Run the API on the Mac at `127.0.0.1:8080`, using standard `net/http`, `http.ServeMux`, and `encoding/json`.
- Use the official typed client-go clientset directly. The application never invokes kubectl or Docker subprocesses.
- Load kubeconfig through client-go loading rules (explicit kubeconfig path, otherwise `KUBECONFIG` / default configuration). Explicitly select context `docker-desktop`; fail startup clearly if configuration, cluster access, or the namespace is unavailable.
- Use one manually created namespace, `deploy-api`. Requests cannot select namespaces or contexts. The API never deletes the namespace.
- The runnable foundation performs only a bounded, read-only startup check: fetch server version and get namespace `deploy-api` within one 10-second overall deadline, then start the HTTP listener. `GET /healthz` returns `200` with `{"status":"ok"}` to confirm the API process is serving; it does not claim ongoing cluster health. Foundation work does not list workloads, inspect diagnostics, or mutate Kubernetes resources.
- Observed on 2026-10-20: cluster Kubernetes `v1.34.1`, node `linux/arm64`, installed Go `1.26.0`, kubectl `1.35.0`.
- Use installed Go 1.26 and Kubernetes module family `v0.34.x`, matching the cluster minor version. Pin `k8s.io/client-go`, `k8s.io/api`, and `k8s.io/apimachinery` to the same exact available patch during implementation; record it in `go.mod`. The inspected `v0.34.1` module requires Go 1.24 or later. Recheck these observations if the environment changes. [Client compatibility](https://github.com/kubernetes/client-go#compatibility-matrix), [module requirements](https://raw.githubusercontent.com/kubernetes/client-go/v0.34.1/go.mod), [standard HTTP routing](https://go.dev/doc/go1.22#enhanced_routing_patterns).

## Application and resource model

An application requires `name`, `image`, and `containerPort`. It must run continuously and listen on one TCP port on the Pod network interface. Images must work with their default entrypoint and configuration. Names are unique within `deploy-api`: 1–63 lowercase letters, digits, or hyphens, starting with a letter and ending with a letter or digit. Ports are integers from 1 to 65535. Images are nonempty public image references; explicit tags or digests are recommended for reproducible exercises.

Each application has:

- One `apps/v1` Deployment named after the app, one container named `app`, and one desired replica. A Deployment asks Kubernetes to keep the desired number of application Pods running; its built-in ReplicaSets manage those Pods.
- One `v1` ClusterIP Service with the same name, TCP port `80`, and `targetPort` equal to `containerPort`. A Service provides a stable internal address and selects this application's Pods by labels. [Service behavior](https://kubernetes.io/docs/concepts/services-networking/service/).
- Labels `app.kubernetes.io/managed-by=deploy-api` and `app.kubernetes.io/name=<name>` on managed resources and Pods; both form the Service and Deployment selectors. Selectors stay fixed. Pod membership is checked through the Deployment → ReplicaSet → Pod ownership chain as well as labels.
- A TCP readiness probe against `containerPort`: period 5 seconds, timeout 1 second, success threshold 1, failure threshold 3. This checks connection acceptance, not application correctness. V1 adds no liveness or startup probes. [Probe semantics](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/).
- Rolling updates with `maxSurge: 1`, `maxUnavailable: 0`, and `progressDeadlineSeconds: 120`. One extra Pod may run during rollout; terminating Pods can temporarily increase the observed count further. Availability depends on readiness and cluster capacity; zero downtime is not guaranteed. A failed rollout is reported without automatic rollback. [Deployment behavior](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/).
- Explicit `imagePullPolicy: Always`. Updating to the identical image reference is a no-op; use a different tag or digest to trigger a rollout.
- Fixed per-container requests: CPU `100m` (one tenth of a CPU), memory `64Mi`; limits: CPU `500m` (half a CPU), memory `128Mi`. Requests inform scheduling; CPU limits can throttle execution and memory limits can lead to `OOMKilled` termination. These are conservative starting budgets for small sample servers, not measured sizing or guarantees for arbitrary images. Every app and replacement Pod gets these values; callers cannot configure them in v1. Budget for the extra rollout Pod as well. [Kubernetes resource semantics](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/).

Kubernetes is the source of truth. List and inspect derive applications from the union of managed Deployments and Services, including partial applications. There is no in-memory application registry or operation history to lose on API restart. Only resources belonging to this API are managed; collisions with unmanaged resources return a conflict.

## HTTP contract

Requests and ordinary responses use JSON. Reject malformed JSON, unknown fields, and invalid values with `400`. Errors have an `error` object containing a stable `code`, readable `message`, and relevant details. Do not return kubeconfig credentials. Mutation success means Kubernetes accepted resource writes, not that the app is ready.

| Endpoint | Request and behavior | Success |
| --- | --- | --- |
| `POST /apps` | Body: `name`, `image`, `containerPort`. Create Deployment, then Service. Existing app names return `409`; this is not an upsert. | `202`, name, namespace, resource names, status URL; `Location: /apps/{name}` |
| `GET /apps` | List managed applications, sorted by name; include resource presence so partial applications remain discoverable. | `200`, `apps` array, empty when none exist |
| `GET /apps/{name}` | Read desired configuration, resource presence, rollout readiness, and diagnostics. | `200`; `404` when neither managed resource exists |
| `PATCH /apps/{name}/image` | Body: `image` only. Change the Deployment container image; keep name, port, selectors, and Service fixed. Return `409` for an incomplete or deleting app. | `202`, name and status URL |
| `GET /apps/{name}/logs?pod=<name>&tailLines=100` | Follow the selected Pod's `app` container; `tailLines` defaults to 100 and accepts 0–1000. Pod selection is required. | `200`, flushed `text/plain` stream |
| `DELETE /apps/{name}` | Request deletion of both managed resources; attempt both even if one fails. Delete Deployment dependents through Kubernetes garbage collection. | `202` while deletion is requested/in progress; `204` if already absent |

Use `404` for missing apps or Pods, `409` for name/resource conflicts or logs unavailable because the container has not started, `503` for Kubernetes unavailability or permission denial, `504` for an upstream timeout, and `500` for unexpected internal errors. Preserve useful Kubernetes reason/message details. Ordinary requests have bounded upstream timeouts (default 10 seconds); never wait for rollout completion. Concurrent Kubernetes write conflicts return `409` and can be retried after inspection.

The ordinary 10-second upstream deadline covers the complete ordinary operation, including its sequential Kubernetes calls. Followed log response bodies are exempt from that overall deadline; timeout middleware, client-go `rest.Config.Timeout`, an underlying HTTP client's total timeout, and HTTP server write deadlines must not cut them off after 10 seconds. Bound ownership lookup and connection/response-header establishment, but do not leave an establishment deadline attached to the running stream. The stream remains tied to the incoming request's cancellation. [Go HTTP timeout semantics](https://pkg.go.dev/net/http#Client).

Creation and deletion span multiple API calls and are not transactions. Keep resources already created if a later step fails. Error details identify each resource's successful, failed, or unknown outcome; do not report `202` for a known failed write. A timeout may leave the outcome unknown: inspect before retrying. Partial applications remain inspectable and deletable; recovery is delete then recreate. Do not add rollback or background repair. Repeated deletion succeeds when resources are already missing; do not delete colliding unmanaged resources. Kubernetes removes owned ReplicaSets and Pods asynchronously. [Garbage collection](https://kubernetes.io/docs/concepts/architecture/garbage-collection/).

### Readiness and useful failures

Inspection returns `name`, namespace, requested image/port, Deployment and Service presence, `ready`, and diagnostics. Include Deployment generation/observed generation, desired/updated/ready/available replica counts, and condition type/status/reason/message. Include each owned Pod's name, deletion timestamp, phase, readiness, actual image reference, container waiting/running/terminated state, reason/message, restart count, and last termination exit code/reason.

`ready=true` requires both resources present and not deleting, the controller having observed the current Deployment generation, the requested rollout complete (one updated and available replica, no old replicas remaining), and its current Pod ready. An old healthy Pod during a failed image update does not make the requested rollout ready. Keep raw diagnostic reasons rather than inventing a permanent app failure state: Kubernetes can keep retrying after `ProgressDeadlineExceeded`.

Include up to 10 most recent warning events associated by UID with the Deployment, its ReplicaSets, or its Pods, with reason/message/timestamp where available. Events are temporary, best-effort evidence; their absence does not mean success. If only the events lookup fails, return the core inspection result with `diagnosticsIncomplete=true` and a readable diagnostic error.

### Log streaming

Validate that the selected Pod belongs to this application's Deployment; reject unrelated Pods. Use client-go's Pod log API with follow enabled and the requested tail count. Open the upstream stream before committing HTTP success so initial failures can return JSON errors. A stream follows one container instance: it ends on EOF, disconnect, or upstream failure; clients explicitly reconnect after restarts or replacement. Do not merge Pods or automatically switch them. Cancel and close the upstream stream when the HTTP client disconnects. After headers are sent, close on error and record it in the API process logs. [Pod log options](https://github.com/kubernetes/api/blob/v0.34.1/core/v1/types.go).

## Manual learning and verification workflow

1. Confirm context, server version, and node OS/architecture with kubectl. Manually create `deploy-api` before starting the API.
2. Before each successful deployment or update, inspect the exact image using `docker buildx imagetools inspect <image>` and confirm a matching `linux/arm64` platform. The API does not preflight registry manifests or guarantee compatibility. Choose a small public HTTP image with a known port and stdout/stderr logs. [Image inspection](https://docs.docker.com/reference/cli/docker/buildx/imagetools/inspect/).
3. Create through HTTP, poll inspection, then compare resources, readiness, events, and ownership with `kubectl get`, `describe`, and `rollout status` in the dedicated namespace. Kubernetes creates ReplicaSets and Pods; the Go API creates Deployment and Service objects.
4. Run `kubectl --context=docker-desktop -n deploy-api port-forward service/<name> 8081:80`, then access `http://127.0.0.1:8081`. Keep that command running. It forwards to a selected backing Pod and may need restarting after replacement; it is not an API feature. [Port forwarding](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/).
5. Select a Pod from inspection and stream logs with an unbuffered HTTP client such as `curl -N`; compare with `kubectl logs -f` for the same Pod.

### Reproducible crashing-container exercise

Use `docker.io/library/postgres:17.6-alpine` solely as a negative diagnostic fixture. Its default entrypoint exits with code 1 on a fresh data directory when `POSTGRES_PASSWORD` is absent. This exercises a process that starts and fails, rather than an image-pull error or a server listening on the wrong port. It does not add a database feature, credentials, storage, or command/environment configuration to this project. [Official image requirements](https://hub.docker.com/_/postgres), [entrypoint failure check](https://github.com/docker-library/postgres/blob/master/docker-entrypoint.sh).

1. Run `docker buildx imagetools inspect docker.io/library/postgres:17.6-alpine`. Confirm `linux/arm64` and record the reported image-index digest. Use `docker.io/library/postgres@sha256:<recorded-digest>` in the request below so the exercise can be repeated with identical image content. If the tag cannot be resolved, stop and choose/document another verified fixture; do not silently substitute `latest`.
2. POST `/apps` with name `crash-demo`, the verified digest reference, and `containerPort: 5432`, leaving its default entrypoint unchanged. Do not supply environment variables or mount a data directory. Expect `202`.
3. Poll GET `/apps/crash-demo` and compare with `kubectl --context=docker-desktop -n deploy-api get pods -l app.kubernetes.io/name=crash-demo -w`. Allow up to 180 seconds after a successful pull to observe retries: `ready=false`, an increasing restart count, last termination exit code `1`, and eventually a waiting reason such as `CrashLoopBackOff`. That reason can alternate with running/terminated states; the Pod phase may still be `Running`.
4. For the Pod named by inspection, run `kubectl --context=docker-desktop -n deploy-api logs <pod> -c app --previous` and confirm the missing-password startup error. Compare the API's termination information with `kubectl describe pod <pod>` in the same context/namespace. Previous-instance logs are a kubectl verification step, not a new API log option; an API stream of the current instance may end quickly or return `409` between restarts.
5. Delete `/apps/crash-demo`; verify its resources disappear and the namespace remains. Do not treat `CrashLoopBackOff`, TCP probe failure, image-pull failure, and `OOMKilled` as interchangeable diagnoses.

## Lightweight automated testing

Use the standard Go testing package and `httptest`; run `go test ./...` for each implementation slice. Keep default tests offline and independent of kubeconfig, Docker Desktop, and registry availability. No additional test framework, coverage target, or cluster provisioning system is required.

- Table-driven tests cover malformed/unknown JSON fields, names and ports, HTTP status/error mapping, and readiness decisions. Include an unobserved generation, an old healthy Pod with an unready replacement, missing resources, and deleting resources.
- Use client-go fake clients/reactors for resource intent and application branching: correct namespace, labels/selectors, fixed budgets/probe/rolling strategy, image-only changes, duplicate/unmanaged conflicts, partial create/delete outcomes, and event lookup degradation. Assert observable behavior rather than mirroring every internal call.
- Fake clients do not run scheduling, admission/defaulting, built-in controllers, image pulls, probes, Service routing, garbage collection, or kubelet restarts. Passing fake-client tests is not proof of actual Kubernetes deployment behavior. [Official fake-client limitations](https://github.com/kubernetes/client-go/blob/v0.34.1/kubernetes/fake/clientset_generated.go).
- Test streaming against a controllable HTTP upstream or narrow stream stub, not the typed fake client's log method: verify initial errors before success headers, timely flushing, a stream surviving beyond an injected shorter ordinary deadline, and prompt upstream cancellation/closure after disconnect. Use synchronization and short injected deadlines rather than long sleeps. Verify the real 10-second exemption with a live stream lasting at least 15 seconds.
- Keep real Docker Desktop acceptance checks explicit and opt-in. Follow the manual workflow and acceptance criteria below for real readiness, image pulls, rolling updates, crash loops, logs, networking, and cleanup. Record which checks used fakes and which used the real cluster, with commands, image digests, and results. Unit tests and cluster exercises provide different evidence.

## Acceptance criteria

These are requirements for the later implementation, not checks performed during planning.

1. Deploy two differently named, architecture-verified applications. Requests return `202` without waiting for readiness; each eventually has one ready desired replica and its own matching Service. Names, images, ports, labels, selectors, and ownership are explainable using kubectl.
2. Port-forward each Service separately and receive a response from the corresponding application. TCP readiness matches observed listening behavior.
3. Inspect during startup and after success. A bad/nonexistent image reports `ready=false` with useful image-pull state/reason or events. An image that starts but fails its readiness check remains unready. Kubernetes conditions expose a stalled rollout after the configured progress deadline.
4. Update a ready app to a different verified image reference on the same port. Observe an additional Pod during rollout and eventual replacement; Service identity remains stable. Updating to an invalid image leaves the old Pod available where possible, while inspection reports the requested rollout unready. Updating to a valid different reference recovers it.
5. Select a Pod and observe existing tail lines and new stdout/stderr output promptly. A followed stream remains open beyond 10 seconds (verify for at least 15 seconds while its container stays running); disconnecting releases the upstream stream. Missing/unrelated Pods and containers not yet started return useful errors before streaming. Pod replacement requires explicit reconnection.
6. Demonstrate a controlled failure of Service creation after Deployment creation (a targeted client failure injection is sufficient). The error identifies the partial result, list/inspect expose it, and deletion cleans it up. Invalid requests and duplicate names create no unintended resources; apps do not affect each other.
7. Restart the API and list/inspect existing and partial apps successfully. Cluster unavailability produces a clear startup or HTTP error rather than an empty successful result.
8. Delete an app, then verify its Service, Deployment, owned ReplicaSets, and Pods eventually disappear. Repeated deletion is harmless, the other app remains intact, and `deploy-api` remains.
9. Execute the crashing-container exercise and distinguish a nonzero process exit/restart backoff from image-pull and readiness failures using both API diagnostics and kubectl.
10. Verify requests and limits on every created container and its rollout replacements. Run the automated checks and identify which results establish HTTP/application behavior and which establish real Kubernetes behavior.

Implement in small reviewable steps: connection and inspection, creation and access, readiness and diagnostics, logs, image updates, deletion and failure cases. For each step, explain the generated code and independently compare its behavior with Kubernetes before proceeding.

## Exclusions

Frontend, authentication, databases, queues, repository builds, operated registries, private-registry credentials, automatic architecture checks, BYOC, custom controllers/resources, sandboxes, production deployment, API-managed port forwarding, ingress, load balancers, environment variables, custom commands, volumes, scaling, automatic rollback, and log aggregation/reconnection. Built-in Kubernetes controllers remain responsible for convergence. This planning task ends with this document; application code and repository scaffolding are deferred.

## Local dashboard extension

The local browser dashboard presents the implemented API lifecycle at `GET /`. It serves embedded static assets from the Go process and calls the existing application endpoints on the same origin. Kubernetes remains the source of application state. The dashboard supports creation, inventory and inspection, image updates, explicit selected-Pod log streams, and confirmed deletion. It displays asynchronous convergence and diagnostics without adding workload configuration or changing API mutation semantics.
