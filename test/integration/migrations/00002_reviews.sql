-- +goose Up
CREATE TABLE product_reviews (
    id          BIGSERIAL PRIMARY KEY,
    product_id  BIGINT NOT NULL REFERENCES products(id)  ON DELETE CASCADE,
    merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    rating      INT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX product_reviews_product_id_idx  ON product_reviews (product_id);
CREATE INDEX product_reviews_merchant_id_idx ON product_reviews (merchant_id);

-- +goose Down
DROP TABLE IF EXISTS product_reviews;
