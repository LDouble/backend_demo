ALTER TABLE errand_tasks
    DROP COLUMN contact_ciphertext,
    DROP COLUMN contact_type;

ALTER TABLE marketplace_listings
    DROP COLUMN contact_ciphertext,
    DROP COLUMN contact_type;
