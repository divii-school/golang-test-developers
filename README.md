# bank-api

A small banking REST API written in Go as the Week 1 deliverable for Divii's Go adoption plan.

## What does it do?

It manages bank accounts over an HTTP API backed by PostgreSQL.

- `GET /health` — liveness check
- `POST /accounts` — create an account (`{"owner": "...", "currency": "..."}`), returns the created account with its ID, balance, and timestamp
- `GET /accounts/{id}` — fetch a single account
- `GET /accounts` — list accounts

Accounts persist to PostgreSQL (via `pgx` through `database/sql`), with schema managed by SQL migrations in `db/migrations/`. Every query takes the request's `context.Context`, so if a client disconnects the database work is cancelled instead of running to completion for nobody.

### Running it

```sh
# database
export DATABASE_URL="postgres://user:pass@localhost:5432/bank?sslmode=disable"
# apply db/migrations/000001_init_schema.up.sql to the database, then:
go run ./cmd/server
```

The server listens on `:8000`.

```sh
curl -X POST localhost:8000/accounts -d '{"owner":"saikat","currency":"INR"}'
curl localhost:8000/accounts/1
```

### Project layout

```
cmd/server/        main.go — HTTP handlers, wiring
internal/account/  the Account domain type
internal/storage/  Postgres connection + account queries
db/migrations/     SQL schema migrations
```

## Where did Claude get it right, and where did I have to fix its output?

**What it got right:**

- The project layout (`cmd/` for the binary, `internal/` for packages that shouldn't be imported from outside) — I would not have known this convention coming from other ecosystems.
- Using `QueryRowContext` with `INSERT ... RETURNING` so the create endpoint returns the full row (ID and `created_at` included) in one round trip instead of an insert followed by a select.
- Passing `r.Context()` down into the storage layer instead of `context.Background()`, so request cancellation actually propagates to the database driver.

**What I had to fix or push back on:**

- Its first version suggested a heavier setup (a router framework and an ORM). The assignment said `net/http` with no framework, and Go 1.22+ method-pattern routing (`"POST /accounts"`, `"GET /accounts/{id}"`) turned out to be enough on its own.
- It initially validated the request body after calling the store; I moved validation (`owner`/`currency` required) before touching the database.
- It returned raw database errors to the client. I changed handlers to log the real error server-side and return a generic message, since internal errors shouldn't leak schema details over HTTP.

## Which Go concept confused me most, and what do I understand now that I didn't on Monday?

**Pointers, and value vs pointer receivers.** Coming from languages where every object is implicitly a reference, Go making the copy explicit tripped me up.

On Monday I didn't understand why `AccountStore` methods are declared as `func (s *AccountStore) Create(...)` instead of `func (s AccountStore) Create(...)`, or why it even mattered. Now I understand:

- A **value receiver** gets a *copy* of the struct. Mutating it inside the method changes the copy, and the change is silently thrown away — no error, just wrong behavior. This is the classic trap for people coming from JS/Python.
- A **pointer receiver** operates on the original. For `AccountStore` the pointer receiver also means every call shares the *same* `*sql.DB` handle (and its connection pool) rather than copying the struct around.
- The rule of thumb I've settled on: use pointer receivers when the method mutates state or the struct holds a shared resource (like a DB handle); value receivers are fine for small, immutable data. And a type should be consistent — not a mix of both.

Related things that clicked along the way: `error` is just a value you check with `if err != nil` and hand upward — there's no exception machinery to unwind — and `defer db.Close()` runs on the way out of `main` no matter how it exits, which is Go's answer to `finally`.
