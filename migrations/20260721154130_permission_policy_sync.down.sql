-- module: notice
DROP TABLE IF EXISTS permission_policy_outbox;
ALTER TABLE casbin_rule DROP INDEX idx_casbin_managed, DROP COLUMN managed;
