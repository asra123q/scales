# Scales

A small HTTP load balancer CLI written in Go, built with [Cobra](https://github.com/spf13/cobra).

## Overview

Scales starts an HTTP reverse proxy that distributes requests across a pool of backend servers. It periodically health-checks each backend and routes traffic only to the ones currently reporting healthy, using a configurable load balancing strategy.

## Installation

```
go install github.com/asra123q/scales@latest
```

Or build from source:

```
git clone https://github.com/asra123q/scales.git
cd scales
go build -o scales .
```

## Usage

```
$ scales serve --port 3000 --backends localhost:8081,localhost:8082,localhost:8083 --strategy round-robin
load balancer (RoundRobin) listening on :3000
server: 1 reached successfully
server: 2 reached successfully
server: 3 reached successfully
```

### Flags

| Flag                    | Default                                             | Description                                          |
| ------------------------ | ---------------------------------------------------- | ----------------------------------------------------- |
| `-p, --port`             | `3000`                                              | Port for the load balancer to listen on               |
| `-b, --backends`         | `localhost:8081,localhost:8082,localhost:8083`      | Backend servers as a comma-separated `host:port` list |
| `-s, --strategy`         | `round-robin`                                       | Load balancing strategy: `round-robin` or `least-connections` |
| `--health-interval`      | `10s`                                               | Interval between backend health checks                |

Requests to the load balancer's listening port are proxied to a healthy backend chosen by the selected strategy; if no backend is healthy, it responds with `503 Service Unavailable`.

## Load balancing strategies

- **round-robin** — cycles through healthy backends in order.
- **least-connections** — routes each request to the healthy backend with the fewest active connections.

## Sample backend servers

The [servers/](servers/) directory contains three identical standalone HTTP servers (`server-1`, `server-2`, `server-3`) that implement a minimal in-memory snippet store, listening on ports 8081, 8082, and 8083 respectively. They exist to give `scales serve` something to load balance against during local testing:

```
POST   /snippets       create a snippet
GET    /snippets/{id}  fetch a snippet
DELETE /snippets/{id}  delete a snippet
```

Run each with `go run ./servers/server-1` (and `server-2`, `server-3`), then point `scales serve` at all three with the default `--backends` flag.

## Project structure

```
.
├── main.go              # entrypoint
├── cmd/
│   ├── root.go           # root Cobra command
│   └── serve.go          # `serve` command: load balancer, health checks, proxying
└── servers/
    ├── server-1/          # sample backend on :8081
    ├── server-2/          # sample backend on :8082
    └── server-3/          # sample backend on :8083
```

## License

No license has been chosen for this project yet.
