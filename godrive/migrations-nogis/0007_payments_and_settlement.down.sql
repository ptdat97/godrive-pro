DROP INDEX IF EXISTS ledger_settlement_idx;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS settlement_batch_id;
DROP TABLE IF EXISTS settlement_items;
DROP TABLE IF EXISTS settlement_batches;
DROP TABLE IF EXISTS payment_transactions;
