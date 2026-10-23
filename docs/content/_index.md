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
  Reproducible Application State as Code
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-10">
{{< hextra/hero-subtitle >}}
  Describe the state you want in HCL and Orchard builds it: inserting database rows, calling APIs, running scripts, and wiring outputs between steps automatically.
{{< /hextra/hero-subtitle >}}
</div>

<div class="homepage-hero-buttons">
{{< hextra/hero-button text="Get Started" link="docs/getting-started/" >}}
{{< hextra/hero-button text="GitHub" link="https://github.com/pthm/orchard" style="background: #000;" >}}
</div>

</div>
<div class="homepage-hero-demo">

<p class="homepage-demo-label">scenarios/hello.hcl</p>

```hcl
action "postgres_query" "merchant" {
  query = "INSERT INTO merchants (name) VALUES ('Acme') RETURNING id"
}

output "merchant_id" {
  value = action.postgres_query.merchant.id
}
```

<p class="homepage-demo-label">Terminal</p>

```sh
orchard plan scenarios/hello.hcl
orchard run  scenarios/hello.hcl
```

</div>
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Scenario Language"
    link="docs/concepts/scenarios/"
    subtitle="Describe the state you want — a merchant with three failed payments, an org with SSO enabled and a suspended user — and Orchard composes the rows, calls, and side effects that produce it."
    icon="document-text"
  >}}
  {{< hextra/feature-card
    title="Reproducible by Design"
    link="docs/concepts/components/"
    subtitle="Same inputs, same state. Components are reusable building blocks; scenarios compose them into complete environments you can stand up, tear down, and share."
    icon="refresh"
  >}}
  {{< hextra/feature-card
    title="Pluggable Providers"
    link="docs/providers/"
    subtitle="Built-in providers for Postgres, HTTP, and exec, plus a protocol for writing your own."
    icon="puzzle"
  >}}
{{< /hextra/feature-grid >}}

<div class="homepage-section-gap"></div>

<div class="homepage-cta">
<div class="homepage-cta-text">

{{< hextra/hero-section >}}
  Get Started in Minutes
{{< /hextra/hero-section >}}

<p class="homepage-cta-lead">Install the CLI, scaffold a project, and run your first scenario against a local database.</p>

<div class="homepage-hero-buttons">
{{< hextra/hero-button text="Your First Scenario" link="docs/getting-started/first-scenario/" >}}
{{< hextra/hero-button text="View on GitHub" link="https://github.com/pthm/orchard" style="background: #000;" >}}
</div>

</div>
<div class="homepage-hero-demo">

<p class="homepage-demo-label">Terminal</p>

```sh
brew install pthm/tap/orchard
orchard init my-project && cd my-project
orchard plan scenarios/hello.hcl --var dsn="postgres://localhost/dev"
orchard run  scenarios/hello.hcl --var dsn="postgres://localhost/dev"
```

</div>
</div>
