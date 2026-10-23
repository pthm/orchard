---
title: Orchard
layout: hextra-home
---

<div class="homepage-hero">
<div class="homepage-hero-text">

{{< hextra/hero-badge link="https://github.com/pthm/orchard" >}}
  <div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
  <span>Free and open source on GitHub</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Seed More Than Your Database
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-10">
{{< hextra/hero-subtitle >}}
  Describe your application state once, in reusable HCL components. Orchard builds it with the tools you already have, from databases to APIs to scripts, and tears it down when you're done. One definition for your demos, tests, and local development.
{{< /hextra/hero-subtitle >}}
</div>

<div class="homepage-hero-buttons">
{{< hextra/hero-button text="Get Started" link="docs/getting-started/" >}}
{{< hextra/hero-button text="GitHub" link="https://github.com/pthm/orchard" style="background: #000;" >}}
</div>

</div>
<div class="homepage-hero-demo">

<p class="homepage-demo-label">scenarios/checkout.hcl</p>

```hcl
component "merchant" "store" {
  source   = "../components/merchant.hcl"
  for_each = { eu = "Acme Europe", us = "Acme America" }
  inputs   = { name = each.value, region = each.key }
}

action "http_request" "subscribe" {
  method = "POST"
  url    = "/subscriptions"
  body   = jsonencode({
    merchant_id = component.merchant.store["eu"].id
  })
}

action "exec_run" "reindex" {
  command = "./scripts/reindex.sh"
  args    = ["${component.merchant.store["eu"].id}"]
}
```

<p class="homepage-demo-label">Terminal</p>

```sh
orchard plan scenarios/checkout.hcl
orchard run  scenarios/checkout.hcl
```

</div>
</div>

<!-- Use cases: a tinted band, four columns -->
<section class="orchard-uses">

<p class="orchard-eyebrow">Built for</p>

<div class="orchard-uses-grid">
<a class="orchard-use" href="docs/guides/demo-seeding/">
  <h3>Demo data</h3>
  <p>A database that tells a believable story: customers with histories, orders with realistic timing, payments with a mix of outcomes.</p>
  <span class="orchard-use-link">Seeding a demo {{< icon name="arrow-right" attributes="height=14" >}}</span>
</a>
<a class="orchard-use" href="docs/guides/integration-testing/">
  <h3>Test datasets</h3>
  <p>Shared, versioned fixtures for unit and integration tests. Each test gets a predictable slice of state from a scenario.</p>
  <span class="orchard-use-link">Integration testing {{< icon name="arrow-right" attributes="height=14" >}}</span>
</a>
<a class="orchard-use" href="docs/guides/local-dev/">
  <h3>Local environments</h3>
  <p>Clone the repo, run one command, and get a working environment with enough data to develop against.</p>
  <span class="orchard-use-link">Local development {{< icon name="arrow-right" attributes="height=14" >}}</span>
</a>
<a class="orchard-use" href="providers/">
  <h3>Automation</h3>
  <p>Multi-step changes across complex systems: database writes, API calls, and scripts in one plan, run in dependency order.</p>
  <span class="orchard-use-link">Providers {{< icon name="arrow-right" attributes="height=14" >}}</span>
</a>
</div>

</section>

<!-- How it fits together: sections hang off a vertical branch -->
<section class="orchard-flow">

{{< hextra/hero-section >}}
  How It Fits Together
{{< /hextra/hero-section >}}

<div class="orchard-flow-item">
<div class="orchard-flow-text">

<p class="orchard-eyebrow"><code>component</code></p>

<h3>Reusable units of state</h3>

<div class="content">

A component is like a function: typed inputs, outputs, and the actions that create its state. It also says how to undo them.

Build a library of components (a merchant, a subscription, a user with SSO) and every scenario can reuse them.

</div>
</div>
<div class="content">

```hcl
component "merchant" {
  variable "name"   { type = string }
  variable "region" {
    type    = string
    default = "us"
  }

  action "postgres_query" "create" {
    query = <<-SQL
      INSERT INTO merchants (name, region)
      VALUES ('${var.name}', '${var.region}')
      RETURNING id, name
    SQL

    lifecycle {
      teardown {
        query = "DELETE FROM merchants WHERE id = ${self.id}"
      }
    }
  }

  output "id"   { value = action.postgres_query.create.id }
  output "name" { value = action.postgres_query.create.name }
}
```

</div>
</div>

<div class="orchard-flow-item">
<div class="orchard-flow-text">

<p class="orchard-eyebrow"><code>scenario</code></p>

<h3>One scenario per application state</h3>

<div class="content">

A scenario names the providers it talks to, the components it uses, and what it hands back.

It's more than database fixtures. Mix in API calls and scripts, reference one step's output in another, and Orchard works out the order. `for_each` and `count` turn one block into many.

</div>
</div>
<div class="content">

```hcl
scenario "checkout" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    http     = { source = "builtin/http" }
    exec     = { source = "builtin/exec" }
  }

  variable "dsn"     { default = env("DATABASE_URL") }
  variable "api_url" { default = env("API_URL") }

  provider "postgres" { dsn = var.dsn }
  provider "http"     { base_url = var.api_url }

  component "merchant" "store" {
    source   = "../components/merchant.hcl"
    for_each = { eu = "Acme Europe", us = "Acme America" }
    inputs   = { name = each.value, region = each.key }
  }

  action "http_request" "subscribe" {
    method = "POST"
    url    = "/subscriptions"
    body   = jsonencode({
      merchant_id = component.merchant.store["eu"].id
    })
  }

  action "exec_run" "reindex" {
    command = "./scripts/reindex.sh"
    args    = ["${component.merchant.store["eu"].id}"]
  }
}
```

</div>
</div>

<div class="orchard-flow-item">
<div class="orchard-flow-text">

<p class="orchard-eyebrow"><code>orchard plan</code></p>

<h3>See it before it happens</h3>

<div class="content">

Plan type-checks every reference, resolves the dependency graph, and prints what would run, without touching any external system.

Run it in CI to catch a broken scenario before anyone needs it.

</div>
</div>
<div class="content">

```text
$ orchard plan scenarios/checkout.hcl
▸ checkout
  4 actions, 2 components — via exec, http, postgres

component "merchant" "store"["eu"]
  source  ../components/merchant.hcl
  inputs  name, region
  ↳ action "postgres_query" "create"  via postgres

component "merchant" "store"["us"]
  source  ../components/merchant.hcl
  inputs  name, region
  ↳ action "postgres_query" "create"  via postgres

  action "exec_run" "reindex"  via exec
    also after  component.merchant.store["eu"]
  action "http_request" "subscribe"  via http
    also after  component.merchant.store["eu"]

✓ Plan is valid
```

</div>
</div>

<div class="orchard-flow-item">
<div class="orchard-flow-text">

<p class="orchard-eyebrow"><code>orchard run</code> · <code>orchard teardown</code></p>

<h3>Build it, then clear it away</h3>

<div class="content">

Run builds the state and writes a record of everything it did. Hand that record to teardown and Orchard undoes each step in reverse dependency order, so foreign keys unwind cleanly.

</div>
</div>
<div class="content">

```sh
$ orchard run scenarios/checkout.hcl
$ ls orchard_checkout_run_*.json
orchard_checkout_run_20261023T103045Z_a3f29e5b.json

$ orchard teardown orchard_checkout_run_20261023T103045Z_a3f29e5b.json
```

</div>
</div>

</section>

<!-- Features: plain grid, icon tiles, no cards -->
<section class="orchard-features">
<a class="orchard-feature" href="docs/concepts/components/">
  <span class="orchard-feature-icon">{{< icon name="cube" attributes="height=20" >}}</span>
  <h3>Components</h3>
  <p>Reusable units of state with typed inputs and outputs, instead of copied seed scripts.</p>
</a>
<a class="orchard-feature" href="docs/concepts/wiring/">
  <span class="orchard-feature-icon">{{< icon name="link" attributes="height=20" >}}</span>
  <h3>Automatic wiring</h3>
  <p>Reference one step's output in another and Orchard builds the dependency graph.</p>
</a>
<a class="orchard-feature" href="docs/concepts/plan-vs-run/">
  <span class="orchard-feature-icon">{{< icon name="eye" attributes="height=20" >}}</span>
  <h3>Plan before run</h3>
  <p>Type-check references and preview the graph without side effects, locally or in CI.</p>
</a>
<a class="orchard-feature" href="docs/concepts/lifecycle/">
  <span class="orchard-feature-icon">{{< icon name="trash" attributes="height=20" >}}</span>
  <h3>Teardown</h3>
  <p>Every run writes a record. Teardown replays it in reverse and keeps going if one step fails.</p>
</a>
<a class="orchard-feature" href="docs/concepts/wiring/#repetition">
  <span class="orchard-feature-icon">{{< icon name="duplicate" attributes="height=20" >}}</span>
  <h3>Repetition</h3>
  <p><code>for_each</code> and <code>count</code> turn one block into many: a merchant per region, three failed payments.</p>
</a>
<a class="orchard-feature" href="providers/">
  <span class="orchard-feature-icon">{{< icon name="puzzle" attributes="height=20" >}}</span>
  <h3>Pluggable providers</h3>
  <p>Postgres, HTTP, and exec built in. Any binary that speaks the JSON-over-stdio protocol can be a provider.</p>
</a>
</section>

<!-- Install -->
<section class="orchard-install">
<div>

{{< hextra/hero-section >}}
  Install
{{< /hextra/hero-section >}}

<p class="orchard-lead">One binary, no daemon or server. It reads HCL files and talks to the systems you point it at.</p>

<a class="orchard-install-link" href="docs/getting-started/first-scenario/">Write your first scenario {{< icon name="arrow-right" attributes="height=16" >}}</a>

</div>
<div class="content">

{{< tabs >}}

{{< tab name="Homebrew" >}}
```sh
brew install pthm/tap/orchard
```
{{< /tab >}}

{{< tab name="Scoop" >}}
```sh
scoop bucket add pthm https://github.com/pthm/scoop-bucket
scoop install pthm/orchard
```
{{< /tab >}}

{{< tab name="Go" >}}
```sh
go install github.com/pthm/orchard@latest
```
{{< /tab >}}

{{< tab name="Binaries" >}}
Archives for Linux, macOS, and Windows, plus `.deb` and `.rpm` packages, are on every [GitHub release](https://github.com/pthm/orchard/releases).
{{< /tab >}}

{{< /tabs >}}

</div>
</section>
