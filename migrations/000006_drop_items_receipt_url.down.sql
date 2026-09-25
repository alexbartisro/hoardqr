-- Restores the column shape only; values dropped by the up migration are gone.
ALTER TABLE items ADD COLUMN receipt_url TEXT;
