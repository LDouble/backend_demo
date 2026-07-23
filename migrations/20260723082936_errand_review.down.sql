-- module: errand
ALTER TABLE errand_tasks DROP COLUMN reviewed_at;
ALTER TABLE errand_tasks DROP COLUMN reviewed_by;
ALTER TABLE errand_tasks DROP COLUMN review_reason;
ALTER TABLE errand_tasks DROP COLUMN review_status;
