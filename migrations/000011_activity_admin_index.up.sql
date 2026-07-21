-- 000011_activity_admin_index.up.sql
-- The composite index for ListAdmin uses columns (review_status, status,
-- start_at). The hot path filters by `status` and orders by `start_at`;
-- the previous column order forced the optimizer to ignore the composite
-- index and fall back to a single-column scan + filesort. The new order
-- (status, review_status, start_at) matches both the WHERE prefix and the
-- ORDER BY suffix so the optimizer can use the index for the entire query.

ALTER TABLE activities
    DROP INDEX idx_activity_admin_list,
    ADD KEY idx_activity_admin_list (status, review_status, start_at);
