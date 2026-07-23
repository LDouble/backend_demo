-- module: core
ALTER TABLE users
    DROP KEY idx_users_union_id,
    DROP KEY uk_users_app_open,
    DROP COLUMN union_id,
    DROP COLUMN open_id,
    DROP COLUMN app_id;
