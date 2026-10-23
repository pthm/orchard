provider "s3" {
  version  = "1"
  protocol = "1"

  description = "Amazon S3, S3-compatible object stores (MinIO, R2, LocalStack), and Google Cloud Storage."

  # `$${...}` escapes HCL interpolation so code-sample-looking text in
  # free-form fields passes through to docs output literally.
  notes = <<-MD
    - Credentials come from the standard AWS chain unless set in the config:
      environment variables, `AWS_PROFILE` and shared config (SSO included), web
      identity, and container and instance roles.
    - With `gcs = true` the provider talks to Google Cloud Storage's XML API and
      authenticates with Application Default Credentials (`gcloud auth
      application-default login`, a service-account key file, or workload identity).
      Set `access_key_id` and `secret_access_key` to use HMAC keys instead.
    - `orchard teardown` undoes the actions automatically, unless an action has its
      own `lifecycle { teardown { } }` block or sets `lifecycle { managed_teardown =
      false }`. A bucket or object the scenario created is deleted. Before
      overwriting an object, `s3_put_object` copies it to
      `.orchard-backup/<run>/<key>` in the same bucket; teardown copies it back when
      an earlier action in the run created the object, or with `managed_teardown =
      true`, and deletes the backup either way. Other overwritten objects are left
      as written. Without automatic teardown the backup stays in the bucket.
    - Buckets are deleted only when empty, so objects the scenario didn't create keep
      a created bucket alive through teardown.
  MD

  config {
    attribute "region" {
      type        = string
      description = "Region for requests and new buckets. Defaults to `us-east-1`, or `auto` with `gcs`. R2 uses `auto`."
    }
    attribute "endpoint" {
      type        = string
      description = "Base URL of an S3-compatible store, such as `http://localhost:9000` for MinIO."
    }
    attribute "path_style" {
      type        = bool
      default     = false
      description = "Address buckets as `host/bucket` rather than `bucket.host`. MinIO and LocalStack usually need `true`."
    }
    attribute "profile" {
      type        = string
      description = "Shared-config profile to use instead of `AWS_PROFILE`."
    }
    attribute "access_key_id" {
      type        = string
      description = "Static access key (an HMAC key with `gcs`). Set with `secret_access_key`."
    }
    attribute "secret_access_key" {
      type        = string
      description = "Static secret key. Pass it through a variable or `env()`."
    }
    attribute "session_token" {
      type        = string
      description = "Session token for temporary static credentials."
    }
    attribute "gcs" {
      type        = bool
      default     = false
      description = "Talk to Google Cloud Storage, with Application Default Credentials unless HMAC keys are set."
    }
    attribute "project" {
      type        = string
      description = "Google Cloud project for new buckets. Only with `gcs`."
    }
  }

  action "s3_create_bucket" {
    description = <<-MD
      Create a bucket. With `exists_ok` (the default), a bucket you already own is
      used as it is, with `created = false`. A name someone else owns is an error.
    MD

    attribute "bucket" {
      type        = string
      required    = true
      description = "Bucket name."
    }
    attribute "exists_ok" {
      type        = bool
      default     = true
      description = "Whether a bucket that already exists is fine."
    }

    output {
      type = object({
        bucket  = string,
        created = bool,
      })
      description = <<-MD
        | Field | Type | Description |
        |-------|------|-------------|
        | `bucket` | `string` | The bucket name. |
        | `created` | `bool` | Whether the action created it. |
      MD
    }

    example {
      description = "Make sure a local MinIO has the uploads bucket."
      code        = <<-HCL
        action "s3_create_bucket" "uploads" {
          bucket = "app-uploads"
        }
      HCL
    }
  }

  action "s3_put_object" {
    description = <<-MD
      Upload an object from inline text, base64, or a file next to the scenario. Set
      exactly one of `content`, `content_base64` and `file`. Files are streamed, not
      read into memory.
    MD

    attribute "bucket" {
      type        = string
      required    = true
      description = "Bucket name."
    }
    attribute "key" {
      type        = string
      required    = true
      description = "Object key."
    }
    attribute "content" {
      type        = string
      description = "Object content as text."
    }
    attribute "content_base64" {
      type        = string
      description = "Object content, base64-encoded, for small binaries."
    }
    attribute "file" {
      type        = string
      description = "Path to a file to upload, relative to the scenario file."
    }
    attribute "content_type" {
      type        = string
      description = "Content type. Defaults to one guessed from the key's extension, then `application/octet-stream`."
    }
    attribute "metadata" {
      type        = map(string)
      description = "User metadata (`x-amz-meta-*`)."
    }
    attribute "on_conflict" {
      type        = string
      default     = "update"
      description = "What to do when the object already exists: `\"update\"` overwrites it, `\"ignore\"` leaves it and returns its `etag` and `size`."
    }

    output {
      type = object({
        bucket  = string,
        key     = string,
        etag    = string,
        size    = number,
        created = bool,
      })
      description = <<-MD
        | Field | Type | Description |
        |-------|------|-------------|
        | `bucket` | `string` | The bucket name. |
        | `key` | `string` | The object key. |
        | `etag` | `string` | The object's ETag, without quotes. |
        | `size` | `number` | Size in bytes. |
        | `created` | `bool` | Whether the object was new. |
      MD
    }

    example {
      description = "Upload an avatar for a seeded user and store its key on the row."
      code        = <<-HCL
        action "s3_put_object" "avatar" {
          bucket = action.s3_create_bucket.uploads.bucket
          key    = "avatars/$${action.postgres_insert.user.id}.png"
          file   = "./fixtures/avatar.png"
        }

        action "postgres_update" "user_avatar" {
          table = "users"
          set   = { avatar_key = action.s3_put_object.avatar.key }
          where = { id = action.postgres_insert.user.id }
        }
      HCL
    }
  }

  action "s3_delete_object" {
    description = "Delete an object. Deleting one that doesn't exist succeeds."

    attribute "bucket" {
      type        = string
      required    = true
      description = "Bucket name."
    }
    attribute "key" {
      type        = string
      required    = true
      description = "Object key."
    }

    output {
      type        = object({})
      description = "None."
    }
  }

  action "s3_delete_bucket" {
    description = "Delete an empty bucket."

    attribute "bucket" {
      type        = string
      required    = true
      description = "Bucket name."
    }

    output {
      type        = object({})
      description = "None."
    }
  }
}
