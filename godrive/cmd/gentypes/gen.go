package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const modulePath = "github.com/example/godrive"

var (
	timeType = reflect.TypeOf(time.Time{})
	rawType  = reflect.TypeOf(json.RawMessage{})
)

// source là chú thích và hằng đọc từ mã nguồn của một package.
type source struct {
	typeDocs  map[string]string            // tên kiểu → chú thích kiểu
	fieldDocs map[string]map[string]string // tên kiểu → tên trường → chú thích
	consts    map[string][]string          // tên kiểu → giá trị hằng, theo thứ tự khai báo
}

type gen struct {
	root    string
	sources map[string]*source // theo import path
	names   map[reflect.Type]string
	queue   []named // struct cần xuất, theo thứ tự
	out     bytes.Buffer
}

// Generate dựng toàn bộ tệp TypeScript. Thứ tự đầu ra tất định: cùng mã
// nguồn luôn cho cùng tệp, nên phép so sánh trong test có ý nghĩa.
func Generate(root string) ([]byte, error) {
	g := &gen{root: root, sources: map[string]*source{}, names: map[reflect.Type]string{}}
	for _, e := range enums {
		g.names[e.t] = e.ts
	}
	for _, r := range roots {
		g.names[r.t] = r.ts
		g.queue = append(g.queue, r)
	}

	g.out.WriteString(header)
	for _, e := range enums {
		if err := g.writeEnum(e); err != nil {
			return nil, err
		}
	}
	// Hàng đợi có thể dài thêm trong lúc duyệt: struct có tên được tham chiếu
	// lần đầu sẽ được nối vào cuối.
	for i := 0; i < len(g.queue); i++ {
		if err := g.writeStruct(g.queue[i]); err != nil {
			return nil, err
		}
	}
	return g.out.Bytes(), nil
}

const header = `// ĐÂY LÀ TỆP SINH TỰ ĐỘNG — ĐỪNG SỬA TAY.
//
// Sinh từ struct Go mà API trả về, bằng godrive/cmd/gentypes:
//
//     cd godrive && make gen-types
//
// Sửa tay sẽ bị ghi đè, và TestGeneratedTypesUpToDate (chạy trong go test)
// báo đỏ khi tệp này lệch với Go. Muốn đổi hình dạng dữ liệu thì đổi struct Go.
`

func (g *gen) writeEnum(e named) error {
	src, err := g.source(e.t.PkgPath())
	if err != nil {
		return err
	}
	vals := src.consts[e.t.Name()]
	if len(vals) == 0 {
		return fmt.Errorf("không tìm thấy hằng nào kiểu %s.%s", e.t.PkgPath(), e.t.Name())
	}
	g.out.WriteString("\n")
	g.out.WriteString(doc("", src.typeDocs[e.t.Name()]))
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = strconv.Quote(v)
	}
	line := fmt.Sprintf("export type %s = %s;\n", e.ts, strings.Join(quoted, " | "))
	if len(line) <= 100 {
		g.out.WriteString(line)
		return nil
	}
	fmt.Fprintf(&g.out, "export type %s =\n", e.ts)
	for i, q := range quoted {
		end := ""
		if i == len(quoted)-1 {
			end = ";"
		}
		fmt.Fprintf(&g.out, "  | %s%s\n", q, end)
	}
	return nil
}

func (g *gen) writeStruct(n named) error {
	src, err := g.source(n.t.PkgPath())
	if err != nil {
		return err
	}
	body, err := g.object(n.t, "")
	if err != nil {
		return err
	}
	g.out.WriteString("\n")
	g.out.WriteString(doc("", src.typeDocs[n.t.Name()]))
	fmt.Fprintf(&g.out, "export interface %s %s\n", n.ts, body)
	return nil
}

// object dựng thân { ... } của một struct, kể cả struct nhúng (JSON làm phẳng
// trường của struct nhúng không có tag — reflection thấy đúng như vậy).
func (g *gen) object(t reflect.Type, indent string) (string, error) {
	var b strings.Builder
	b.WriteString("{\n")
	if err := g.fields(&b, t, indent+"  "); err != nil {
		return "", err
	}
	b.WriteString(indent + "}")
	return b.String(), nil
}

func (g *gen) fields(b *strings.Builder, t reflect.Type, indent string) error {
	src, err := g.source(t.PkgPath())
	if err != nil {
		return err
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			if err := g.fields(b, f.Type, indent); err != nil {
				return err
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		optional := strings.Contains(opts, "omitempty")
		ft := f.Type
		nullable := false
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
			nullable = !optional // con trỏ nil không omitempty → JSON là null
		}
		ts, err := g.typeOf(ft, indent)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
		if nullable {
			ts += " | null"
		}
		q := ""
		if optional {
			q = "?"
		}
		if t.Name() != "" {
			b.WriteString(doc(indent, src.fieldDocs[t.Name()][f.Name]))
		}
		fmt.Fprintf(b, "%s%s%s: %s;\n", indent, name, q, ts)
	}
	return nil
}

func (g *gen) typeOf(t reflect.Type, indent string) (string, error) {
	if name, ok := g.names[t]; ok {
		return name, nil
	}
	switch {
	case t == timeType:
		return "string", nil // RFC 3339
	case t == rawType:
		// Mọi json.RawMessage trong API đều là đối tượng JSON (giá trị cấu hình,
		// siêu dữ liệu sự kiện).
		return "Record<string, unknown>", nil
	}
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		// money.VND cũng về đây: tiền là số nguyên đồng, vừa trong số an toàn
		// của JavaScript (2^53) với mọi số tiền thực tế.
		return "number", nil
	case reflect.Interface:
		return "unknown", nil
	case reflect.Pointer:
		return g.typeOf(t.Elem(), indent)
	case reflect.Slice, reflect.Array:
		inner, err := g.typeOf(t.Elem(), indent)
		if err != nil {
			return "", err
		}
		// Kiểu phức hợp cần ngoặc trước [] — "A | B[]" khác "(A | B)[]".
		if strings.ContainsAny(inner, " |{") {
			inner = "(" + inner + ")"
		}
		return inner + "[]", nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("map có khoá không phải chuỗi: %s", t)
		}
		v, err := g.typeOf(t.Elem(), indent)
		if err != nil {
			return "", err
		}
		return "Record<string, " + v + ">", nil
	case reflect.Struct:
		if t.Name() == "" {
			return g.object(t, indent) // struct vô danh: viết tại chỗ
		}
		// Struct có tên chưa đăng ký: xuất kèm dưới tên Go của nó.
		ts := t.Name()
		for _, used := range g.names {
			if used == ts {
				return "", fmt.Errorf("trùng tên TS %q cho %s — thêm nó vào roots với tên khác", ts, t)
			}
		}
		g.names[t] = ts
		g.queue = append(g.queue, named{ts, t})
		return ts, nil
	}
	return "", fmt.Errorf("chưa hỗ trợ kiểu %s", t)
}

func doc(indent, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		return fmt.Sprintf("%s/** %s */\n", indent, lines[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s/**\n", indent)
	for _, l := range lines {
		fmt.Fprintf(&b, "%s%s\n", indent, strings.TrimRight(" * "+l, " "))
	}
	fmt.Fprintf(&b, "%s */\n", indent)
	return b.String()
}

// source đọc (một lần) chú thích và hằng của package có import path p.
func (g *gen) source(p string) (*source, error) {
	if s, ok := g.sources[p]; ok {
		return s, nil
	}
	s := &source{typeDocs: map[string]string{}, fieldDocs: map[string]map[string]string{}, consts: map[string][]string{}}
	g.sources[p] = s
	rel, ok := strings.CutPrefix(p, modulePath+"/")
	if !ok {
		return s, nil // kiểu thư viện chuẩn: không có chú thích để lấy
	}
	dir := filepath.Join(g.root, filepath.FromSlash(rel))
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		for _, file := range sortedFiles(pkg) {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				switch gd.Tok {
				case token.TYPE:
					collectTypes(s, gd)
				case token.CONST:
					collectConsts(s, gd)
				}
			}
		}
	}
	return s, nil
}

// sortedFiles cho thứ tự tệp ổn định — map của ParseDir không có thứ tự, mà
// hằng của cùng một kiểu có thể nằm rải ở nhiều tệp.
func sortedFiles(pkg *ast.Package) []*ast.File {
	names := make([]string, 0, len(pkg.Files))
	for n := range pkg.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*ast.File, len(names))
	for i, n := range names {
		out[i] = pkg.Files[n]
	}
	return out
}

func collectTypes(s *source, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		ts := spec.(*ast.TypeSpec)
		doc := ts.Doc
		if doc == nil && len(gd.Specs) == 1 {
			doc = gd.Doc
		}
		if doc != nil {
			s.typeDocs[ts.Name.Name] = doc.Text()
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		fd := map[string]string{}
		for _, f := range st.Fields.List {
			text := ""
			if f.Doc != nil {
				text = f.Doc.Text()
			} else if f.Comment != nil {
				text = f.Comment.Text()
			}
			for _, n := range f.Names {
				fd[n.Name] = text
			}
		}
		s.fieldDocs[ts.Name.Name] = fd
	}
}

func collectConsts(s *source, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		id, ok := vs.Type.(*ast.Ident)
		if !ok || len(vs.Values) != len(vs.Names) {
			continue
		}
		for _, v := range vs.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			s.consts[id.Name] = append(s.consts[id.Name], val)
		}
	}
}
