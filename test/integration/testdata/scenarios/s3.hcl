scenario "s3" {
  required_providers {
    postgres = { source = "builtin/postgres" }
    s3       = { source = "builtin/s3" }
  }

  variable "dsn" {}
  variable "endpoint" {}

  provider "postgres" {
    dsn = var.dsn
  }

  provider "s3" {
    endpoint          = var.endpoint
    path_style        = true
    access_key_id     = "minioadmin"
    secret_access_key = "minioadmin"
  }

  action "postgres_insert" "merchant" {
    table  = "merchants"
    values = { name = "Acme" }
  }

  # Created: deleted on teardown, after its objects.
  action "s3_create_bucket" "uploads" {
    bucket = "uploads"
  }

  action "s3_put_object" "import" {
    bucket = action.s3_create_bucket.uploads.bucket
    key    = "imports/${action.postgres_insert.merchant.id}.csv"
    file   = "../fixtures/products.csv"
  }

  action "s3_put_object" "inline" {
    bucket       = action.s3_create_bucket.uploads.bucket
    key          = "notes/readme"
    content      = "sku,qty\nW-1,10\n"
    content_type = "text/csv"
    metadata     = { source = "orchard" }
  }

  # A 1x1 PNG.
  action "s3_put_object" "pixel" {
    bucket         = action.s3_create_bucket.uploads.bucket
    key            = "pixel.png"
    content_base64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
  }

  # Owned: overwritten, so restored from its backup before it's deleted.
  action "s3_put_object" "owned" {
    depends_on = [action.s3_put_object.inline]
    bucket     = action.s3_create_bucket.uploads.bucket
    key        = "notes/readme"
    content    = "changed"
  }

  # Pre-existing bucket: kept on teardown.
  action "s3_create_bucket" "existing" {
    bucket = "existing"
  }

  # Pre-existing object, restored because it's asked for.
  action "s3_put_object" "requested" {
    bucket  = action.s3_create_bucket.existing.bucket
    key     = "requested.txt"
    content = "ours"

    lifecycle {
      managed_teardown = true
    }
  }

  # Pre-existing object, left as written; only its backup is removed.
  action "s3_put_object" "unowned" {
    bucket  = action.s3_create_bucket.existing.bucket
    key     = "unowned.txt"
    content = "ours"
  }

  # Pre-existing, ignored: nothing written.
  action "s3_put_object" "ignored" {
    bucket      = action.s3_create_bucket.existing.bucket
    key         = "ignored.txt"
    content     = "ours"
    on_conflict = "ignore"
  }

  action "postgres_update" "merchant_import" {
    table = "merchants"
    set   = { region = action.s3_put_object.import.key }
    where = { id = action.postgres_insert.merchant.id }
  }

  output "merchant_id"      { value = action.postgres_insert.merchant.id }
  output "import_key"       { value = action.s3_put_object.import.key }
  output "import_size"      { value = action.s3_put_object.import.size }
  output "bucket_created"   { value = action.s3_create_bucket.uploads.created }
  output "existing_created" { value = action.s3_create_bucket.existing.created }
  output "owned_created"    { value = action.s3_put_object.owned.created }
  output "ignored_created"  { value = action.s3_put_object.ignored.created }
  output "ignored_size"     { value = action.s3_put_object.ignored.size }
  output "pixel_etag"       { value = action.s3_put_object.pixel.etag }
}
