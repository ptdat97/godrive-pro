package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// setPath đặt giá trị vào JSON của một nhóm theo đường dẫn dấu chấm.
func setPath(t *testing.T, raw json.RawMessage, path string, val any) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			t.Fatalf("đường dẫn %q: %q không phải object", path, p)
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = val
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hasPath(raw json.RawMessage, path string) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = obj[p]
		if !ok {
			return false
		}
	}
	return true
}

// Mọi đường dẫn trong lược đồ phải tồn tại thật trong JSON của nhóm.
//
// Một đường dẫn gõ sai không làm hỏng gì ngay: giao diện chỉ vẽ ra một ô rỗng
// mà sửa không ăn thua, và người dùng sẽ nghĩ hệ thống bỏ qua thay đổi của họ.
func TestSchemaPathsExistInDefaults(t *testing.T) {
	for _, k := range AllKeys {
		raw, err := marshalDefault(k)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, sec := range SchemaFor(k) {
			if len(sec.Fields) == 0 {
				t.Errorf("%s: khối %q không có ô nào", k, sec.Title)
			}
			for _, fl := range sec.Fields {
				if !hasPath(raw, fl.Path) {
					t.Errorf("%s: đường dẫn %q không có trong JSON của nhóm", k, fl.Path)
				}
				if seen[fl.Path] {
					t.Errorf("%s: đường dẫn %q xuất hiện hai lần", k, fl.Path)
				}
				seen[fl.Path] = true
				if fl.Label == "" {
					t.Errorf("%s: %q thiếu nhãn", k, fl.Path)
				}
			}
		}
	}
}

// Và ngược lại: mọi trường có thể sửa đều phải xuất hiện trên giao diện.
//
// Thiếu chiều này thì thêm một trường vào groups.go mà quên khai báo lược đồ sẽ
// tạo ra một cấu hình chỉ sửa được bằng cách gọi API tay.
func TestEveryFieldIsEditableInUI(t *testing.T) {
	for _, k := range AllKeys {
		raw, err := marshalDefault(k)
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]any
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		covered := map[string]bool{}
		for _, sec := range SchemaFor(k) {
			for _, fl := range sec.Fields {
				covered[strings.Split(fl.Path, ".")[0]] = true
			}
		}
		for name := range top {
			if !covered[name] {
				t.Errorf("%s: trường %q sửa được nhưng không có trên giao diện", k, name)
			}
		}
	}
}

// validates cho biết một giá trị có qua được Validate của nhóm hay không.
func validates(k Key, raw json.RawMessage) error {
	snap := Defaults()
	return decodeInto(k, raw, &snap)
}

// Ràng buộc liên trường khiến vài ô không thể chạm tới cực trị của chính nó khi
// các ô khác đang ở mặc định. Đây không phải lỗi lược đồ — ghi ra để người đọc
// sau không tưởng là bỏ sót.
var boundExceptions = map[string]string{
	"initial_radius_m@max": "bán kính vòng đầu không được vượt bán kính tối đa (mặc định 5000m)",
	"max_radius_m@min":     "bán kính tối đa không được nhỏ hơn bán kính vòng đầu (mặc định 1500m)",
	"max_permille@min":     "trần tăng giá không được thấp hơn hệ số của bậc thang mặc định (1200)",
}

// Ngưỡng công bố cho giao diện phải KHỚP ngưỡng Validate thực thi.
//
// Lệch một chiều thì người chỉnh gõ đúng theo hướng dẫn trên màn hình rồi bị máy
// chủ từ chối; lệch chiều kia thì giao diện chặn một giá trị vốn hợp lệ. Cả hai
// đều làm người dùng mất niềm tin vào màn hình cấu hình.
func TestSchemaBoundsMatchValidation(t *testing.T) {
	for _, k := range AllKeys {
		base, err := marshalDefault(k)
		if err != nil {
			t.Fatal(err)
		}
		for _, sec := range SchemaFor(k) {
			for _, fl := range sec.Fields {
				if fl.Min == nil || fl.Max == nil {
					continue // bool và bảng bậc thang không có ngưỡng số
				}
				name := fl.Path[strings.LastIndex(fl.Path, ".")+1:]
				step := 1.0
				if fl.Kind == KindFloat {
					step = 0.5
				}

				t.Run(string(k)+"/"+fl.Path, func(t *testing.T) {
					// Đúng ngưỡng thì phải nhận.
					if _, skip := boundExceptions[name+"@min"]; !skip {
						if err := validates(k, setPath(t, base, fl.Path, *fl.Min)); err != nil {
							t.Errorf("giá trị nhỏ nhất %g bị từ chối: %v", *fl.Min, err)
						}
					}
					if _, skip := boundExceptions[name+"@max"]; !skip {
						if err := validates(k, setPath(t, base, fl.Path, *fl.Max)); err != nil {
							t.Errorf("giá trị lớn nhất %g bị từ chối: %v", *fl.Max, err)
						}
					}
					// Ngoài ngưỡng thì phải chặn.
					if err := validates(k, setPath(t, base, fl.Path, *fl.Min-step)); err == nil {
						t.Errorf("giá trị %g dưới ngưỡng nhưng được chấp nhận", *fl.Min-step)
					}
					if err := validates(k, setPath(t, base, fl.Path, *fl.Max+step)); err == nil {
						t.Errorf("giá trị %g trên ngưỡng nhưng được chấp nhận", *fl.Max+step)
					}
				})
			}
		}
	}
}
