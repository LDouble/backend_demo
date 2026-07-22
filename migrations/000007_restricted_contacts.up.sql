ALTER TABLE marketplace_listings
    ADD COLUMN contact_type VARCHAR(16) NOT NULL DEFAULT 'phone' AFTER owner_id,
    ADD COLUMN contact_ciphertext VARCHAR(4096) NOT NULL DEFAULT '' AFTER contact_type;

ALTER TABLE errand_tasks
    ADD COLUMN contact_type VARCHAR(16) NOT NULL DEFAULT 'phone' AFTER requester_id,
    ADD COLUMN contact_ciphertext VARCHAR(4096) NOT NULL DEFAULT '' AFTER contact_type;
