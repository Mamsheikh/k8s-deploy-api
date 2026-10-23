# k8s-deploy-api

A small Go HTTP API for deploying and managing containerized applications on a local Kubernetes cluster. It uses the Kubernetes Go client to create a Deployment and a ClusterIP Service for each app.

The API supports creating and listing apps, inspecting rollout readiness and Pod diagnostics, reading warning events, streaming Pod logs, updating container images, and deleting apps.

## Run locally

You need Go 1.26.0 or later, kubectl, and Docker Desktop with Kubernetes enabled. Your kubeconfig must contain the `docker-desktop` context.

Create the namespace once:

```sh
kubectl --context=docker-desktop create namespace deploy-api
```

Start the API from the project directory:

```sh
go mod download
go run .
```

The server checks cluster access and the namespace before listening at `http://127.0.0.1:8080`. It always uses context `docker-desktop` and namespace `deploy-api`.

To use a specific kubeconfig file, run `go run . -kubeconfig /path/to/config`.

## Deploy an app

With the server running, send a container image and its listening port:

```sh
curl -H 'Content-Type: application/json' \
  -d '{"name":"hello","image":"nginx:stable-alpine","containerPort":80}' \
  http://127.0.0.1:8080/apps
```

Creation returns `202 Accepted`. Check the app's status to see when its rollout is ready:

```sh
curl http://127.0.0.1:8080/apps/hello
```

To access the app locally, forward its Service port, then open `http://127.0.0.1:8081`:

```sh
kubectl --context=docker-desktop -n deploy-api port-forward service/hello 8081:80
```

## API endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Check that the API process is serving |
| POST | `/apps` | Create an app |
| GET | `/apps` | List managed apps |
| GET | `/apps/{name}` | Inspect status, readiness, Pods, and warning events |
| PATCH | `/apps/{name}/image` | Update the image with `{"image":"<image-reference>"}` |
| GET | `/apps/{name}/logs?pod=<pod-name>&tailLines=100` | Follow logs for a selected owned Pod |
| DELETE | `/apps/{name}` | Delete the app's Deployment and Service |

## Scope

This project is a local development tool. The API has no authentication and binds to the loopback address. Apps use one replica and an internal ClusterIP Service. Kubernetes resources hold the app state, so apps remain discoverable after the API restarts.

## Development

Run the tests without a live cluster:

```sh
go test ./...
```

See the [runbook](docs/runbook.md) for more examples and troubleshooting, and the [specification](docs/spec.md) for detailed behavior.
