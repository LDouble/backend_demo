-- 000009_activity_registration_idempotency.down.sql
ALTER TABLE activity_registrations
    DROP INDEX uk_activity_registration_idempotency,
    DROP COLUMN idempotency_key;
