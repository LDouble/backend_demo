ALTER TABLE permission_policy_outbox
    ADD COLUMN locked_at DATETIME(3) NULL AFTER dispatched_at,
    ADD COLUMN locked_by VARCHAR(64) NOT NULL DEFAULT '' AFTER locked_at,
    ADD INDEX idx_permission_policy_claim (dispatched_at, locked_at, id);
