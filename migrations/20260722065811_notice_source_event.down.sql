-- module: notice
ALTER TABLE notices
  DROP INDEX uk_notices_source_event_id,
  DROP COLUMN source_event_id;
