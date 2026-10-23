---
title: Manual UAT testing
weight: 20
---

**Goal.** Reproducible test cases for QA and stakeholders. "A subscription with
three failed payments" should be one command, not a fifteen-minute click-through.

**Why Orchard.** UAT scripts usually live as a README page of SQL snippets, a
shared Notion doc, or inside someone's head. Turning them into scenarios makes
them runnable, version-controlled, and reviewable.

## Pattern: test case per scenario

Each distinct UAT case becomes a scenario file. Name them after the test case:

```
scenarios/
├── uat/
│   ├── subscription_three_failures.hcl
│   ├── refund_across_currencies.hcl
│   ├── merchant_account_locked.hcl
│   └── high_risk_chargeback.hcl
```

## Example: subscription with three failures

```hcl
scenario "uat_subscription_three_failures" {
  required_providers {
    postgres = { source = "builtin/postgres" }
  }

  variable "dsn" {}

  variable "customer_email" {
    default = "uat+subs3fail@example.com"
  }

  provider "postgres" {
    dsn = var.dsn
  }

  component "customer" "subject" {
    source = "../../components/customer.hcl"
    inputs = {
      email = var.customer_email
      name  = "UAT Customer"
    }
  }

  component "subscription" "plan" {
    source = "../../components/subscription.hcl"
    inputs = {
      customer_id = component.customer.subject.id
      plan        = "pro_monthly"
      status      = "past_due"
    }
  }

  action "postgres_query" "failed_attempt_1" {
    query = "INSERT INTO payment_attempts (subscription_id, status, failure_code, attempted_at) VALUES (${component.subscription.plan.id}, 'failed', 'card_declined', NOW() - INTERVAL '14 days')"
  }

  action "postgres_query" "failed_attempt_2" {
    query = "INSERT INTO payment_attempts (subscription_id, status, failure_code, attempted_at) VALUES (${component.subscription.plan.id}, 'failed', 'insufficient_funds', NOW() - INTERVAL '7 days')"
  }

  action "postgres_query" "failed_attempt_3" {
    query = "INSERT INTO payment_attempts (subscription_id, status, failure_code, attempted_at) VALUES (${component.subscription.plan.id}, 'failed', 'card_declined', NOW() - INTERVAL '1 day')"
  }

  output "customer_email"  { value = component.customer.subject.email }
  output "subscription_id" { value = component.subscription.plan.id }
}
```

A QA engineer runs this and gets a login email plus the exact state described in
the test plan.

## Tips

- **Unique identifiers per run.** Use variables with sensible defaults, but
  override them for each run so tests don't collide:

  ```sh
  orchard run scenarios/uat/subscription_three_failures.hcl \
    --var customer_email="uat+$(date +%s)@example.com"
  ```

- **Surface what QA needs** in outputs: customer ID, login URL, API token. Don't
  make testers run a second SQL query to find the row they just created.
- **Use `lifecycle { teardown { } }` instead of cleanup scripts.** Attach a
  teardown block to each `action` and let `orchard teardown <record>` undo the
  run:

  ```hcl
  action "postgres_query" "create_subscription" {
    query = "INSERT INTO subscriptions (...) RETURNING id"
    lifecycle {
      teardown {
        query = "DELETE FROM subscriptions WHERE id = ${self.id}"
      }
    }
  }
  ```

  The record file `orchard run` writes is all `orchard teardown` needs — no
  bespoke cleanup scenario, no hand-rolled shell script. See
  [lifecycle](../../concepts/lifecycle/).
- **Run UAT scenarios in CI.** Even if the real tests are manual, having CI
  verify that each scenario still applies cleanly catches schema drift early.
