-- module: notice
ALTER TABLE notice_deliveries DROP INDEX idx_delivery_lease;
ALTER TABLE notice_deliveries DROP COLUMN locked_at;
