// Package money biểu diễn tiền tệ VND bằng số nguyên (đồng).
// KHÔNG dùng float cho tiền: sai số làm lệch đối soát.
package money

import (
	"fmt"
	"strings"
)

// VND là số tiền tính bằng đồng Việt Nam (đơn vị nhỏ nhất, không phần lẻ).
type VND int64

func FromInt(v int64) VND { return VND(v) }

func (v VND) Int64() int64  { return int64(v) }
func (v VND) IsZero() bool  { return v == 0 }
func (v VND) Neg() VND      { return -v }
func (v VND) Add(o VND) VND { return v + o }
func (v VND) Sub(o VND) VND { return v - o }

// MulDiv nhân với num rồi chia cho den, làm tròn nửa ra xa số 0.
//
// Đây là phép nguyên thuỷ để mọi phép tỉ lệ trên tiền chạy bằng SỐ NGUYÊN.
// Dùng float ở đây (kể cả biến tạm) sẽ tạo sai lệch từng đồng, và sai lệch
// một đồng trong đối soát tiền mặt đủ làm mất niềm tin của tài xế.
//
// den phải dương — mọi mẫu số trong hệ thống là hằng số nghiệp vụ (1000 phần
// nghìn, 60 giây/phút), nên mẫu số âm hoặc bằng 0 là lỗi lập trình, không phải
// dữ liệu xấu.
func (v VND) MulDiv(num, den int64) VND {
	if den <= 0 {
		panic("money: mẫu số phải dương")
	}
	n := int64(v) * num
	// Go cắt về phía 0, nên nửa phải cộng theo đúng dấu của tử số.
	if n < 0 {
		return VND((n - den/2) / den)
	}
	return VND((n + den/2) / den)
}

// MulPermille nhân với tỉ lệ phần nghìn để tránh float.
// Ví dụ chiết khấu 20% -> rate = 200.
func (v VND) MulPermille(rate int64) VND {
	return v.MulDiv(rate, 1000)
}

// RoundTo làm tròn lên bội số gần nhất (giá cước VN thường tròn 1.000đ).
func (v VND) RoundTo(step VND) VND {
	if step <= 0 {
		return v
	}
	r := v % step
	if r == 0 {
		return v
	}
	if v > 0 {
		return v + (step - r)
	}
	return v - r
}

func (v VND) Clamp(min, max VND) VND {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// String định dạng kiểu 1.250.000₫
func (v VND) String() string {
	n := int64(v)
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	raw := fmt.Sprintf("%d", n)
	var sb strings.Builder
	for i, c := range raw {
		if i > 0 && (len(raw)-i)%3 == 0 {
			sb.WriteByte('.')
		}
		sb.WriteRune(c)
	}
	return sign + sb.String() + "\u20AB"
}
