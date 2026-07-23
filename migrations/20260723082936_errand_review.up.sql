-- module: errand
ALTER TABLE errand_tasks ADD COLUMN review_status VARCHAR(24) NOT NULL DEFAULT 'approved';
ALTER TABLE errand_tasks ADD COLUMN review_reason VARCHAR(500) NULL;
ALTER TABLE errand_tasks ADD COLUMN reviewed_by BIGINT UNSIGNED NULL;
ALTER TABLE errand_tasks ADD COLUMN reviewed_at DATETIME(3) NULL;
ALTER TABLE errand_tasks ALTER COLUMN review_status DROP DEFAULT;
