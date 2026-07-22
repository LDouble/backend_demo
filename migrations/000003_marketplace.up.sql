CREATE TABLE marketplace_listings (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, title VARCHAR(200) NOT NULL, description LONGTEXT NOT NULL,
    price_cents BIGINT NOT NULL, currency VARCHAR(3) NOT NULL, status VARCHAR(24) NOT NULL,
    rejection_reason VARCHAR(500) NULL, owner_id BIGINT UNSIGNED NOT NULL, reviewed_by BIGINT UNSIGNED NULL,
    reviewed_at DATETIME(3) NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), KEY idx_marketplace_listings_status (status), KEY idx_marketplace_listings_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE marketplace_listing_images (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, listing_id BIGINT UNSIGNED NOT NULL, url VARCHAR(2048) NOT NULL, position BIGINT NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_listing_image_position (listing_id, position),
    CONSTRAINT fk_marketplace_listing_image FOREIGN KEY (listing_id) REFERENCES marketplace_listings(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE trade_orders (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, order_no VARCHAR(40) NOT NULL, order_type VARCHAR(32) NOT NULL,
    resource_type VARCHAR(64) NOT NULL, resource_id BIGINT UNSIGNED NOT NULL, buyer_id BIGINT UNSIGNED NOT NULL, seller_id BIGINT UNSIGNED NOT NULL,
    amount_cents BIGINT NOT NULL, currency VARCHAR(3) NOT NULL, payment_mode VARCHAR(16) NOT NULL, trade_status VARCHAR(24) NOT NULL,
    fulfillment_status VARCHAR(24) NOT NULL, title_snapshot VARCHAR(200) NOT NULL, resource_snapshot JSON NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL, expires_at DATETIME(3) NULL, paid_at DATETIME(3) NULL, completed_at DATETIME(3) NULL,
    cancelled_at DATETIME(3) NULL, closed_at DATETIME(3) NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_trade_orders_order_no (order_no), UNIQUE KEY uk_trade_buyer_idempotency (buyer_id, idempotency_key),
    KEY idx_trade_resource (resource_type, resource_id), KEY idx_trade_expiry (trade_status, expires_at),
    KEY idx_trade_orders_buyer_id (buyer_id), KEY idx_trade_orders_seller_id (seller_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE trade_order_transitions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, order_id BIGINT UNSIGNED NOT NULL, from_status VARCHAR(24) NOT NULL,
    to_status VARCHAR(24) NOT NULL, actor_type VARCHAR(16) NOT NULL, actor_id BIGINT UNSIGNED NULL,
    reason_code VARCHAR(64) NULL, reason_text VARCHAR(500) NULL, idempotency_key VARCHAR(128) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_trade_transition_idempotency (idempotency_key), KEY idx_trade_transition_order (order_id),
    CONSTRAINT fk_trade_transition_order FOREIGN KEY (order_id) REFERENCES trade_orders(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE marketplace_reservations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, listing_id BIGINT UNSIGNED NOT NULL, trade_order_id BIGINT UNSIGNED NOT NULL,
    buyer_id BIGINT UNSIGNED NOT NULL, status VARCHAR(16) NOT NULL, expires_at DATETIME(3) NOT NULL, version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_marketplace_reservation_order (trade_order_id), KEY idx_reservation_expiry (status, expires_at),
    CONSTRAINT fk_marketplace_reservation_listing FOREIGN KEY (listing_id) REFERENCES marketplace_listings(id) ON DELETE RESTRICT,
    CONSTRAINT fk_marketplace_reservation_order FOREIGN KEY (trade_order_id) REFERENCES trade_orders(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE domain_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, aggregate_type VARCHAR(64) NOT NULL, aggregate_id BIGINT UNSIGNED NOT NULL,
    event_type VARCHAR(64) NOT NULL, payload_version BIGINT UNSIGNED NOT NULL, payload JSON NOT NULL, idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL, available_at DATETIME(3) NOT NULL, locked_at DATETIME(3) NULL, attempts BIGINT NOT NULL DEFAULT 0,
    last_error VARCHAR(1000) NULL, created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id), UNIQUE KEY uk_domain_events_idempotency_key (idempotency_key), KEY idx_domain_events_dispatch (status, available_at, locked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
