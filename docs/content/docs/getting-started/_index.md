---
title: Getting started
weight: 10
bookCollapseSection: true
---

# Getting started

Three short pages that take you from nothing installed to a working scenario.

1. [Installation](installation/) — get the `orchard` binary on your machine
2. [Setup](setup/) — prerequisites and project layout
3. [First scenario](first-scenario/) — write and run your first HCL scenario

**Quick start.** If you just want to see something work:

```sh
go install github.com/pthm/orchard@latest
orchard init my-project
cd my-project
orchard plan scenarios/hello.hcl --var dsn="postgres://localhost/dev"
```

`orchard init` scaffolds a starter scenario and component so you don't have to
write anything from scratch. The pages below explain what it generated and how
to build on it.
