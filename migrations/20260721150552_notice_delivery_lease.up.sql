-- module: notice
ALTER TABLE notice_deliveries ADD COLUMN locked_at DATETIME(3) NULL;
ALTER TABLE notice_deliveries ADD INDEX idx_delivery_lease (status, locked_at);
