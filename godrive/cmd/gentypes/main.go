// Command gentypes sinh kiểu TypeScript cho godrive-admin từ chính struct Go
// mà API trả về.
//
// Trước đây godrive-admin/src/lib/types.ts được chép tay, và đã trôi: lần đầu
// chạy bộ sinh này, phía TS thiếu hẳn những trường Go đã trả từ lâu. Kiểu chép
// tay sai thì trình biên dịch không báo gì — nó chỉ tin vào bản chép.
//
// Cách làm: hình dạng struct đọc bằng reflection (đúng thứ encoding/json dùng,
// nên tên trường, omitempty, nhúng struct đều khớp); chú thích và giá trị enum
// đọc từ mã nguồn bằng go/ast, vì reflection không thấy được hai thứ đó.
//
//	go run ./cmd/gentypes                 # ghi đè ../godrive-admin/src/lib/types.ts
//	go run ./cmd/gentypes -check          # chỉ so sánh, khác thì thoát mã 1
//
// TestGeneratedTypesUpToDate chạy -check trong `go test ./...`.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	out := flag.String("out", defaultOut(), "tệp TypeScript cần ghi")
	check := flag.Bool("check", false, "chỉ kiểm tra tệp có khớp không, không ghi")
	flag.Parse()

	src, err := Generate(moduleRoot())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(2)
	}
	if *check {
		cur, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gentypes:", err)
			os.Exit(2)
		}
		if !bytes.Equal(cur, src) {
			fmt.Fprintf(os.Stderr, "gentypes: %s đã lệch với struct Go — chạy `make gen-types`\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gentypes:", err)
		os.Exit(2)
	}
}

// moduleRoot là thư mục godrive/ — nơi chứa go.mod, tính từ vị trí tệp này
// để chạy được từ bất kỳ thư mục nào.
func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func defaultOut() string {
	return filepath.Join(moduleRoot(), "..", "godrive-admin", "src", "lib", "types.ts")
}
