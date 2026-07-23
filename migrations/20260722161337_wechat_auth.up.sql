-- module: core
ALTER TABLE users
    ADD COLUMN app_id VARCHAR(64) NULL AFTER username,
    ADD COLUMN open_id VARCHAR(64) NULL AFTER app_id,
    ADD COLUMN union_id VARCHAR(64) NULL AFTER open_id,
    ADD UNIQUE KEY uk_users_app_open (app_id, open_id),
    ADD KEY idx_users_union_id (union_id);
