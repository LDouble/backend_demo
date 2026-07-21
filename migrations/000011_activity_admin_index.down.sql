-- 000011_activity_admin_index.down.sql
ALTER TABLE activities
    DROP INDEX idx_activity_admin_list,
    ADD KEY idx_activity_admin_list (review_status, status, start_at);
