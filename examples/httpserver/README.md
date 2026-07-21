# httpserver

Example server that uses all three packages in this repository:

- `netdev` registers the host network driver under TinyGo
- `httpmux` provides method-aware routes and path wildcards
- `httprevproxy` proxies `/proxy/*` requests to an upstream server

## Run with standard Go

```bash
go run ./examples/httpserver
```

## Build & run with TinyGo

```bash
tinygo build -o server ./examples/httpserver
./server
```

The listen address defaults to `:8080`. Override it with `ADDR`:

```bash
ADDR=:9090 ./server
```

The proxy target defaults to `http://127.0.0.1:8081`. Override it with
`UPSTREAM_URL`; a base path is supported:

```bash
UPSTREAM_URL=http://127.0.0.1:9000/api ./server
```

## Try it

```bash
curl -s http://127.0.0.1:8080/
curl -s http://127.0.0.1:8080/healthz
curl -s -X POST --data 'hello tinygo' http://127.0.0.1:8080/echo
curl -s http://127.0.0.1:8080/proxy/users/42
```

## Routes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Hello + request / runtime info |
| GET, HEAD | `/healthz` | Liveness (`ok`) |
| POST | `/echo` | Echo request body (max 64 KiB) |
| Any | `/proxy/{path...}` | Reverse proxy to `UPSTREAM_URL`, stripping `/proxy/` |

Methods that do not match a route receive `405 Method Not Allowed` with an
`Allow` header from `httpmux`.
