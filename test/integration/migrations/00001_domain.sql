-- +goose Up
CREATE TABLE merchants (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    region     TEXT NOT NULL DEFAULT 'us',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE products (
    id          BIGSERIAL PRIMARY KEY,
    merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    price       NUMERIC(12, 2) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX products_merchant_id_idx ON products (merchant_id);

-- +goose Down
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS merchants;
