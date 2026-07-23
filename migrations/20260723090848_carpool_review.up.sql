-- module: carpool
ALTER TABLE carpool_trips ADD COLUMN review_status VARCHAR(24) NOT NULL DEFAULT 'approved';
ALTER TABLE carpool_trips ADD COLUMN review_reason VARCHAR(500) NULL;
ALTER TABLE carpool_trips ADD COLUMN reviewed_by BIGINT UNSIGNED NULL;
ALTER TABLE carpool_trips ADD COLUMN reviewed_at DATETIME(3) NULL;
ALTER TABLE carpool_trips ALTER COLUMN review_status DROP DEFAULT;
