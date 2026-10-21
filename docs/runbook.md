# Run the connection and health foundation

This slice starts a local HTTP process after two read-only Kubernetes checks. It fetches the server version and gets namespace `deploy-api` under one overall 10-second deadline. Health confirms that the HTTP process is serving. It does not recheck Kubernetes.

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

Expect `200`, `Content-Type: application/json`, and `{"status":"ok"}` for the first request. Expect `405` for the second and `404` for the third. Application endpoints are not implemented in this slice.

Press Ctrl-C in the API terminal to release the port. Restart and repeat the health request.

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
