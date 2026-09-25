-- Receipts are PDFs or photos kept in a dedicated document manager
-- (paperless-ngx), not a free-text link on the item, so the column has no
-- job left. Dropped rather than hidden: nothing reads or writes it anymore.
ALTER TABLE items DROP COLUMN receipt_url;
