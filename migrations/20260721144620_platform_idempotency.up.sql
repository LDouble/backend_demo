-- module: trade
CREATE TABLE idempotency_records (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    actor_id BIGINT UNSIGNED NOT NULL,
    operation_id VARCHAR(128) NOT NULL,
    key_hash CHAR(64) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    http_status INT NOT NULL DEFAULT 0,
    response_body BLOB NULL,
    resource_type VARCHAR(64) NULL,
    resource_id BIGINT UNSIGNED NULL,
    completed_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_idempotency_scope (actor_id, operation_id, key_hash),
    KEY idx_idempotency_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
