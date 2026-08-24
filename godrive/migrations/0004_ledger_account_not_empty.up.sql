-- 0004 — Chốt chặn ở tầng CSDL: bút toán phải có chủ.
--
-- wallet.Transaction.Validate() đã chặn ở tầng ứng dụng, nhưng sổ cái là nơi
-- không được phép tin vào một lớp phòng thủ duy nhất: một bút toán vô chủ nằm
-- trong tổng doanh thu mà không thuộc ví của ai thì không bao giờ đối soát được,
-- và vì bảng chỉ INSERT nên không có cách nào sửa lại ngoài ghi bút toán đảo.
ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_account_id_not_empty CHECK (account_id <> '');
ALTER TABLE ledger_transactions
    ADD CONSTRAINT ledger_transactions_tx_id_not_empty CHECK (tx_id <> '');
