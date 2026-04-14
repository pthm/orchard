provider "postgres" {
  version  = "1"
  protocol = "1"

  description = "PostgreSQL data operations"

  config {
    attribute "dsn" {
      type        = string
      required    = true
      description = "PostgreSQL connection string (e.g. postgres://user:pass@host:5432/db)"
    }
  }

  action "sql" {
    description = "Execute a SQL statement. The first row returned (typically from a RETURNING clause) is exposed as outputs keyed by column name."
    attribute "query" {
      type        = string
      required    = true
      description = "The SQL statement to execute"
    }
    output {
      type        = any
      description = "The first row of the result set, keyed by column name. Null if no rows were returned."
    }
  }
}
