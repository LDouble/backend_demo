-- module: trade
ALTER TABLE idempotency_records
    ADD COLUMN response_headers JSON NULL AFTER response_body;
