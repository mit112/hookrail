-- enforced only after ingest (Task 7) writes endpoint_id on every insert and
-- the 0002 backfill populated existing rows (design §5 sequencing).
ALTER TABLE deliveries ALTER COLUMN endpoint_id SET NOT NULL;
