-- module: notice
ALTER TABLE casbin_rule
  ADD COLUMN managed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD INDEX idx_casbin_managed (managed, ptype, v0);

CREATE TABLE permission_policy_outbox (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  version VARCHAR(64) NOT NULL,
  attempts BIGINT NOT NULL DEFAULT 0,
  dispatched_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_permission_policy_version (version),
  KEY idx_permission_policy_pending (dispatched_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
