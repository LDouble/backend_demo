-- module: trade
ALTER TABLE idempotency_records
    DROP COLUMN response_headers;
