provider "postgres" {
  version  = "1"
  protocol = "1"

  description = "PostgreSQL data operations"

  config {
    attribute "dsn" {
      type        = string
      required    = true
      description = "PostgreSQL connection string"
    }
    attribute "max_conns" {
      type    = number
      default = 10
    }
  }

  action "sql" {
    description = "Execute a raw SQL query and capture the first row"
    attribute "query" {
      type     = string
      required = true
    }
    output {
      type = any
    }
  }

  action "insert" {
    description = "Insert a row with schema-aware validation"
    attribute "table" {
      type     = string
      required = true
    }
    attribute "values" {
      type     = map(any)
      required = true
    }
    attribute "returning" {
      type    = list(string)
      default = []
    }
    output {
      type = object({ id = number })
    }
  }
}
