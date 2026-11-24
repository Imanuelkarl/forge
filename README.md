# goforge

A Nest-CLI-style scaffolder for Go backends that follow the MogRex Backend Engineering Standard
(Chi, `database/sql`, golang-migrate, slog, validator, swaggo).

The command is `goforge` (not `forge`, which clashes with Laravel Forge's CLI). The project file
is still `forge.yaml` and the manifest still lives in `.forge/`.

## Install

```bash
# from anywhere (needs Go 1.22+)
go install github.com/Imanuelkarl/forge/cmd/goforge@latest

# or from a checkout
make install          # = go install ./cmd/goforge, stamps the version from git

goforge --version
```

`go install` puts the binary in `$(go env GOPATH)/bin` (`%USERPROFILE%\go\bin` on Windows):
make sure that directory is on your `PATH`.

## Commands

| Command                                                 | What it does                                                                                                                                                                                                                                                  |
| ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `goforge new <name>`                                    | Prompts for module path, database (postgres/mysql), swagger. Writes the project skeleton, `forge.yaml`, `.env`, Makefile, Dockerfile, migration runner; runs `go mod tidy`.                                                                                   |
| `goforge init`                                          | Run inside an existing project. Reads the module from `go.mod`, detects the database, writes `forge.yaml` and only the missing skeleton files: never overwrites or edits your files. See below.                                                               |
| `goforge generate module <name>` (`g m`)                | Reads the module from `forge.yaml`, or prompts for fields (or `--field`), appends it to `forge.yaml`, and generates domain + DTOs, handler (CRUD + custom endpoints, swagger), service, SQL repository, in-memory repository, tests, and the table migration. |
| `goforge generate middleware <name>` (`g mw`)           | Scaffolds a middleware (`blank`, `security-headers`, `rate-limit`) and registers it globally.                                                                                                                                                                 |
| `goforge sync`                                          | Re-reads `forge.yaml`, regenerates every module and the wiring.                                                                                                                                                                                               |
| `goforge migrate up\|down\|status\|goto\|force\|create` | Runs migrations through the project's `cmd/migrate` (golang-migrate). Same as `make migrate-up`, etc.                                                                                                                                                         |
| `goforge doctor`                                        | `gofmt`, `go build`, `go vet`, `go test`, `golangci-lint` (if installed).                                                                                                                                                                                     |

Global flags: `--dry-run`, `--force`. Per-command: `--no-swagger`, `-y/--yes`; `new` also takes
`--db postgres|mysql` and `--id-type int|bigint|uuid`.

## Adding goforge to an existing project

```bash
cd my-existing-api
git status            # start clean, so you can review what goforge adds
goforge init          # or: goforge init -y --db postgres
```

`init` works in the current directory and never touches what is already there:

- **Existing files are kept.** A skeleton file that already exists (`README.md`, `cmd/server/main.go`,
  `internal/config/config.go`, ...) is left as it is, and goforge keeps leaving it alone on every later `sync`.
- **Your packages stay yours.** If a skeleton package such as `internal/config` already holds your own code,
  goforge adds nothing to it. It also holds back any skeleton file that would need goforge's version of that
  package (e.g. `internal/database`).
- **No clashes.** A new Go file is not written when its package name differs from the directory's, or when it
  declares a name your package already declares.
- **`go.mod`** is only changed by `go mod tidy`, which adds goforge's dependencies (you are asked first;
  `--skip-tidy` skips it). Existing migrations stay; new ones get later timestamps.

Everything that was kept or held back is listed at the end. Re-running `goforge init` adds whatever is still
missing (e.g. after you rename a clashing function).

If you keep your own server and router, mount the generated modules once:

```go
h := app.NewHandler(app.Deps{DB: db, Logger: logger, Validate: validator.New()})
r.Route("/api/v1", h.Routes) // every module goforge generates
app.RegisterGlobalMiddleware(r) // optional: middleware listed in forge.yaml
```

From then on use goforge as usual: `goforge g m <name>`, `goforge sync`, `goforge migrate up`.

Not built yet: auth, rbac, notifications, monitoring/audit packs; `migrate diff`; `layout: feature`;
soft delete; enforcing `security:` rules (they are parsed and recorded).

## forge.yaml

```yaml
version: 1
project:
  name: orders-api
  module: github.com/acme/orders-api
  layout: layered
  database: postgres # postgres | mysql
  swagger: true
  id_type: int # primary key of every module: int | bigint | uuid (default int)

middleware:
  - security-headers

modules: # keep this LAST; `goforge g m` appends to it
  order:
    id: uuid # optional: override project.id_type for this module
    fields:
      customer_id: { type: int, required: true, ref: customers.id }
      status: { type: enum, values: [pending, paid, shipped], default: pending }
      total: { type: decimal, required: true }
      note: { type: string, nullable: true, max: 500 }
    dto:
      create: [customer_id, total, note]
      update: [status, note]
    endpoints:
      crud: [list, get, create, update, delete]
      custom:
        - {
            name: cancel,
            method: POST,
            path: "/{id}/cancel",
            permission: "order:cancel",
          }
    security:
      permissions: { list: "order:read", create: "order:create" }
      owner_scoped: customer_id
```

Field types: `string text int bigint float decimal bool uuid timestamp enum`.
Field options: `required nullable unique index default min max format(email|url) values validate ref description`.
Module options: `id` (primary key type), `table`, `plural` (for irregular nouns).

Unknown keys are an error (`unnique: true` fails with _did you mean "unique"?_) instead of being ignored.

### Primary keys

| `id_type` / `id` | Column (postgres / mysql)                                           | Go type  | Assigned by                                      |
| ---------------- | ------------------------------------------------------------------- | -------- | ------------------------------------------------ |
| `int` (default)  | `integer GENERATED BY DEFAULT AS IDENTITY` / `int AUTO_INCREMENT`   | `int64`  | database (`RETURNING id` / `LastInsertId`)       |
| `bigint`         | `bigint GENERATED BY DEFAULT AS IDENTITY` / `bigint AUTO_INCREMENT` | `int64`  | database                                         |
| `uuid`           | `uuid` / `char(36)`                                                 | `string` | service (UUIDv7, `internal/services/ids_gen.go`) |

Handlers reject malformed ids with 400 (non-positive-integer, or not a UUID). A `ref` field must have
the same type as the referenced module's id.

Notes:

- Module and field **order** matters: it is the migration order, and a `ref` becomes a real foreign key only
  if the referenced table is created by a module declared _earlier_.
- `unique` fields must be `required`; `text`, `bool`, `float` and `timestamp` cannot be unique.
- `decimal` is a Go `string` (exact, no float rounding). `uuid` fields are native `uuid` columns on
  postgres (`char(36)` on mysql) and are validated as UUIDs in request DTOs.
- Update DTO fields are pointers, so `false`, `0` and `""` can be set. A nullable field cannot be reset to NULL through the API yet.
- Custom endpoints get a handler and a service **stub** that answers 500 until you implement it.

## Safe regeneration

- Files starting with `// Code generated by goforge. DO NOT EDIT.` (`*_gen.go`) are always rewritten.
- Every other generated file is **yours**. goforge stores a hash of what it wrote in `.forge/manifest.json`
  (commit it) and will not overwrite a file you changed. It lists skipped files at the end; `--force` overrides.
- A module's migration is written once. If you change the module's fields later, goforge tells you to write a
  new migration (`goforge migrate create <name>`) instead of editing an applied one. If the migration was
  never applied, delete its `.up.sql` and run `goforge sync` to regenerate it.
- `goforge sync` also refreshes `cmd/server/main.go`, `internal/handlers/router.go`,
  `health_handler_test.go` and `services/helpers.go` when you have not edited them, so older projects
  pick up wiring changes.

## Migrations

Projects use golang-migrate as a library through `cmd/migrate` (no CLI install needed). Files are
`migrations/<timestamp>_<name>.up.sql` / `.down.sql`.

```bash
make migrate-up | migrate-down n=1 | migrate-status | migrate-create name=x | migrate-force version=N
goforge migrate up
```

Switching database means changing `project.database` for a new project; the dialect layer
(`internal/dialect`) is the only place that knows about SQL differences. MySQL `DATABASE_URL` keeps
the `mysql://` prefix and needs `parseTime=true`.

## Deviations from your template (all deliberate)

1. `NewRouter(logger, h *handlers.Handler, corsOrigins, opts)` receives every handler through one
   `handlers.Handler` struct (generated in `handlers/handler_gen.go`, built by `app.NewHandler(deps)`),
   so adding a module never requires editing `router.go`/`main.go`:

   ```go
   h := app.NewHandler(app.Deps{DB: db, Logger: logger, Validate: validator.New()})
   router := handlers.NewRouter(logger, h, cfg.CORSAllowedOrigins, handlers.RouterOptions{
       Global: app.RegisterGlobalMiddleware, EnableDocs: !cfg.IsProduction(),
   })
   ```

2. `handleDomainError` / validation formatting moved from each handler to `handlers/helpers.go`.
3. List responses are paginated and carry `meta` (`SuccessResponse.Meta`).
4. `config` loads `.env` itself (stdlib loader) and requires `DATABASE_URL`.
5. `database/sql` (pgx stdlib / go-sql-driver) instead of pgxpool, to keep MySQL possible.
6. Readiness (`/readyz`) pings the database and returns 503 when it is down.
7. `http.Server` sets `ReadHeaderTimeout`.
8. Files are normalised to LF.
