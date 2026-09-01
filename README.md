# bank-api

A small banking REST API written in Go for Divii's Go adoption plan — stdlib `net/http` (no framework), PostgreSQL, and a background settlement worker built on a goroutine and a channel.

## What it does

It manages bank accounts and money transfers over an HTTP API backed by PostgreSQL.

Creating a transfer does not settle it inline: the handler persists it as `pending`, enqueues its ID on a buffered channel, and returns `202 Accepted` immediately. A background worker goroutine consumes the queue and settles each transfer in a single database transaction — it locks both accounts in id order (so two concurrent transfers between the same pair can never deadlock), moves the balance, writes double-entry ledger rows, and marks the transfer `completed`, or `failed` with `insufficient funds`. On shutdown the queue is drained before exit; transfers still pending at startup are requeued, so a crash between accept and settle loses nothing.

Every query takes the request's `context.Context`, so if a client disconnects the database work is cancelled instead of running to completion for nobody. Each settlement job gets its own 5-second timeout. Logging is structured JSON via `log/slog`.

## Endpoints

- `GET /health` — liveness check
- `POST /accounts` — create an account (`{"owner": "...", "currency": "..."}`), returns the created account with its ID, balance, and timestamp
- `GET /accounts` — list accounts
- `GET /accounts/{id}` — fetch a single account
- `POST /transfers` — create a transfer (`{"from_account_id": 1, "to_account_id": 2, "amount": 50}`); returns `202` with the pending transfer, settled by the background worker
- `GET /transfers/{id}` — check a transfer's status: `pending`, `completed`, or `failed` (with an error message)

## How to run

Locally:

```sh
# database
export DATABASE_URL="postgres://user:pass@localhost:5432/bank?sslmode=disable"
# apply db/migrations/*.up.sql to the database in order, then:
go run ./cmd/server
```

With Docker:

```sh
docker build -t bank-api .
docker run -p 8000:8000 -e DATABASE_URL="postgres://user:pass@host.docker.internal:5432/bank?sslmode=disable" bank-api
```

The server listens on `:8000`.

```sh
curl -X POST localhost:8000/accounts -d '{"owner":"saikat","currency":"INR"}'
curl -X POST localhost:8000/accounts -d '{"owner":"minkyu","currency":"INR"}'
curl -X POST localhost:8000/transfers -d '{"from_account_id":2,"to_account_id":1,"amount":50}'
curl localhost:8000/transfers/1
```

Tests (no database needed — the storage layer is tested against `go-sqlmock` and the worker against a fake settler):

```sh
go test ./...
```

Project layout:

```
cmd/server/        main.go — HTTP handlers, wiring, graceful shutdown
internal/account/  the Account domain type
internal/transfer/ the Transfer domain type and status constants
internal/worker/   background worker: channel queue + settling goroutine
internal/storage/  Postgres connection, account queries, transfer settlement
db/migrations/     SQL schema migrations
```

## Where Claude was right, and where I had to fix it

**What it got right:**

- The project layout (`cmd/` for the binary, `internal/` for packages that shouldn't be imported from outside) — I would not have known this convention coming from other ecosystems.
- Using `QueryRowContext` with `INSERT ... RETURNING` so the create endpoints return the full row (ID and `created_at` included) in one round trip instead of an insert followed by a select.
- Passing `r.Context()` down into the storage layer instead of `context.Background()`, so request cancellation actually propagates to the database driver.
- Locking the two accounts in id order inside the settlement transaction. I asked why the order mattered and had it show me the deadlock two concurrent A→B and B→A transfers cause without it.

**What I had to fix or push back on:**

- Its first version suggested a heavier setup (a router framework and an ORM). The assignment said `net/http` with no framework, and Go 1.22+ method-pattern routing (`"POST /accounts"`, `"GET /accounts/{id}"`) turned out to be enough on its own.
- It initially validated the request body after calling the store; I moved validation (`owner`/`currency` required, positive amount, distinct accounts) before touching the database.
- It returned raw database errors to the client. I changed handlers to log the real error server-side and return a generic message, since internal errors shouldn't leak schema details over HTTP.
- Its first worker settled the transfer using the request's context. That is wrong for a background job — the request is long gone by the time the job runs — so each job now gets its own `context.WithTimeout`.
- The worker originally depended on the concrete `*storage.TransferStore`, which made it untestable without Postgres. I introduced a one-method `Settler` interface ("accept interfaces, return structs") so tests inject a fake.

## The Go concept that confused me most

**Pointers, and value vs pointer receivers.** Coming from languages where every object is implicitly a reference, Go making the copy explicit tripped me up.

I didn't understand why `AccountStore` methods are declared as `func (s *AccountStore) Create(...)` instead of `func (s AccountStore) Create(...)`, or why it even mattered:

- A **value receiver** gets a *copy* of the struct. Mutating it inside the method changes the copy, and the change is silently thrown away — no error, just wrong behavior. This is the classic trap for people coming from JS/Python.
- A **pointer receiver** operates on the original. For `AccountStore` the pointer receiver also means every call shares the *same* `*sql.DB` handle (and its connection pool) rather than copying the struct around.
- The rule of thumb I've settled on: use pointer receivers when the method mutates state or the struct holds a shared resource (like a DB handle); value receivers are fine for small, immutable data. And a type should be consistent — not a mix of both.

## What I understand now that I did not before

- The receiver rules above — on day one I could not have explained why `*AccountStore` and not `AccountStore`; now I can, and I can predict which one a method needs before writing it.
- **Channel lifecycle and shutdown ordering.** `close(ch)` is a signal from the sender, `for job := range ch` ends when the channel is drained, and *only the sender may close*. That's why `Worker.Stop` is called strictly after `srv.Shutdown` returns: no handler can be left trying to send on a closed channel. The `done` channel is the worker's way of saying "drained" back.
- **`select` as the shape of cancellable blocking.** `Enqueue` blocks on a full queue, but `select { case jobs <- job: ... case <-ctx.Done(): ... }` means it blocks *only as long as the caller is still waiting*. Once I saw that pattern, `context` stopped being boilerplate and became the point.
- `error` is just a value you check with `if err != nil` and hand upward — wrapped with `fmt.Errorf("...: %w", err)` so `errors.Is` still sees `sql.ErrNoRows` through the layers; there's no exception machinery to unwind. And `defer db.Close()` / `defer tx.Rollback()` run on the way out no matter how the function exits — a committed transaction makes the deferred rollback a harmless no-op, which is Go's answer to `finally`.
