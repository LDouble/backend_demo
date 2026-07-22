-- module: notice
ALTER TABLE notices
  ADD COLUMN source_event_id BIGINT UNSIGNED NULL,
  ADD UNIQUE KEY uk_notices_source_event_id (source_event_id);
