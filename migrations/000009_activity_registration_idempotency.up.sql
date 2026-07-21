-- 000009_activity_registration_idempotency.up.sql
-- Adds idempotency_key column + unique index to activity_registrations so that
-- POST /api/v1/activities/{id}/registrations can be replayed safely with the
-- same Idempotency-Key (mirrors errand's trade_orders idempotency_key pattern).

ALTER TABLE activity_registrations
    ADD COLUMN idempotency_key VARCHAR(128) NOT NULL DEFAULT '',
    ADD UNIQUE KEY uk_activity_registration_idempotency (activity_id, user_id, idempotency_key);

-- Existing rows were created without an idempotency key; backfill with a
-- stable per-row value derived from the registration id so the UNIQUE constraint
-- can validate. Subsequent calls for these rows must be reset; cleanest path
-- is to introduce an admin-only reissue flow rather than auto-rebilling.
UPDATE activity_registrations SET idempotency_key = CONCAT('legacy:', id) WHERE idempotency_key = '';

-- After backfill drop the temporary default to lock the column NOT NULL semantics.
ALTER TABLE activity_registrations
    ALTER COLUMN idempotency_key DROP DEFAULT;
