---
title: Integration testing
weight: 40
---

# Integration testing

**Goal.** Shared, versioned setup for your integration test suite. Each test
needs a predictable slice of the database; Orchard produces that slice from a
scenario.

**Why Orchard.** Integration tests tend to accumulate setup code in Go/Python/
whatever — `createMerchant(t, db, "Acme")` functions that duplicate what your
product already does. Orchard scenarios are a source of truth that both tests
and local dev can share.

## Pattern: scenario per test fixture

```
test/
├── integration/
│   ├── fixtures/
│   │   ├── empty.hcl
│   │   ├── one_merchant.hcl
│   │   └── merchant_with_orders.hcl
│   └── ...
```

Each test declares which fixture it needs and runs that scenario as part of
setup.

## From Go tests

```go
func TestMerchantCheckout(t *testing.T) {
    dsn := setupTestDB(t)  // drops + recreates + migrates

    outputs := runOrchard(t, "fixtures/merchant_with_orders.hcl", map[string]string{
        "dsn": dsn,
    })

    merchantID := outputs["merchant_id"].(string)
    // ... test logic against merchantID
}

func runOrchard(t *testing.T, scenario string, vars map[string]string) map[string]any {
    args := []string{"run", scenario}
    for k, v := range vars {
        args = append(args, "--var", fmt.Sprintf("%s=%s", k, v))
    }
    cmd := exec.Command("orchard", args...)
    out, err := cmd.Output()
    if err != nil {
        t.Fatalf("orchard: %v\n%s", err, out)
    }
    // parse outputs from stdout (or use --output json in a future release)
    return parseOrchardOutputs(out)
}
```

## Fast path: run once, tear down between tests

If you're paying the database startup cost per test, switch to a shared DSN and
use transactions or truncation to isolate tests:

```go
func TestMain(m *testing.M) {
    dsn := startSharedDB()
    applyMigrations(dsn)
    runOrchard(nil, "fixtures/base.hcl", map[string]string{"dsn": dsn})
    os.Exit(m.Run())
}

func TestSomething(t *testing.T) {
    tx := beginTx(t)
    defer tx.Rollback()
    // ... test against tx
}
```

The fixture scenario runs once; each test rolls back its own changes.

## Pattern: chaining scenarios

Complex tests sometimes need layered state: "the base fixture, plus a
scenario-specific addition." Write a small scenario that depends on the base:

```hcl
scenario "test_with_chargeback" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}
  variable "merchant_id" {}   # provided by the caller

  provider "postgres" {
    dsn = var.dsn
  }

  action "sql" "add_chargeback" {
    query = "INSERT INTO chargebacks (merchant_id, amount) VALUES (${var.merchant_id}, 100.00) RETURNING id"
  }

  output "chargeback_id" { value = action.sql.add_chargeback.id }
}
```

The test runs the base fixture, captures `merchant_id`, then runs the second
scenario with `--var merchant_id=...`.

## Tips

- **Use `orchard plan` as a smoke test.** CI can plan every fixture scenario to
  catch schema drift before test execution.
- **Tear down eagerly.** Integration DBs are shared — leaks across test runs
  make failures hard to diagnose. Prefer transactional rollback, truncation, or
  per-test databases.
- **Don't reimplement components in Go.** If your tests call
  `createMerchant(t, ...)` for the tenth time, that's a component crying to be
  shared with the `dev.hcl` and `demo_*.hcl` scenarios.
