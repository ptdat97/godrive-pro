package main

import (
	"bytes"
	"os"
	"testing"
)

// T-23: types.ts của godrive-admin phải đúng là thứ bộ sinh tạo ra từ struct
// Go hiện tại. Đổi struct mà quên sinh lại thì test này đỏ — thay cho việc
// giao diện lặng lẽ đọc sai dữ liệu.
func TestGeneratedTypesUpToDate(t *testing.T) {
	path := defaultOut()
	cur, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("không có %s (repo chỉ chứa backend?)", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(moduleRoot())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cur, want) {
		t.Fatalf("%s đã lệch với struct Go. Chạy: cd godrive && make gen-types", path)
	}
}

// Hai lần sinh liên tiếp phải giống hệt nhau — nếu không, test trên sẽ đỏ
// ngẫu nhiên (thứ tự map, thứ tự đọc tệp).
func TestGenerateIsDeterministic(t *testing.T) {
	a, err := Generate(moduleRoot())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := Generate(moduleRoot())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatal("hai lần sinh cho kết quả khác nhau")
		}
	}
}
