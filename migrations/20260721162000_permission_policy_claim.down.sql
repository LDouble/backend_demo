ALTER TABLE permission_policy_outbox
    DROP INDEX idx_permission_policy_claim,
    DROP COLUMN locked_by,
    DROP COLUMN locked_at;
