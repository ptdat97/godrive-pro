// Package id sinh định danh sắp xếp được theo thời gian (kiểu ULID rút gọn).
// Ưu điểm so với UUIDv4: index B-tree của Postgres không bị phân mảnh.
package id

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"strings"
	"time"
)

var enc = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// New trả về chuỗi dạng "trp_01J8ZK...". prefix giúp đọc log dễ hơn.
func New(prefix string) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UTC().UnixMilli()))
	if _, err := rand.Read(b[8:]); err != nil {
		panic("id: nguồn ngẫu nhiên lỗi: " + err.Error())
	}
	s := enc.EncodeToString(b[:])
	if prefix == "" {
		return s
	}
	return prefix + "_" + s
}

// HasPrefix kiểm tra id có đúng loại không (chống nhầm truyền driverID vào tripID).
func HasPrefix(v, prefix string) bool {
	return strings.HasPrefix(v, prefix+"_")
}
