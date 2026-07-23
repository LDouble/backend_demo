-- module: carpool
ALTER TABLE carpool_trips DROP COLUMN reviewed_at;
ALTER TABLE carpool_trips DROP COLUMN reviewed_by;
ALTER TABLE carpool_trips DROP COLUMN review_reason;
ALTER TABLE carpool_trips DROP COLUMN review_status;
