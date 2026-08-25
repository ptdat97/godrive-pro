package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// OSRMETA tính ETA bằng endpoint /table của OSRM: MỘT request cho cả lô ứng
// viên thay vì N request.
//
// Chi phí bản đồ là rủi ro số 4 trong danh sách rủi ro của dự án — hoá đơn API
// có thể vượt cả tiền máy chủ. Với một vòng dispatch 50 ứng viên, /table là 1
// lần tính tiền còn /route từng cặp là 50.
//
// Có cache theo CẶP Ô LƯỚI: hai tài xế cách nhau vài chục mét thì thời gian tới
// điểm đón gần như nhau, nên không cần hỏi lại. Đây là cách giảm hoá đơn hiệu
// quả nhất, và cũng là lý do pkg/geo có sẵn lưới ô.
type OSRMETA struct {
	baseURL string
	http    *http.Client
	// Profile là hồ sơ định tuyến của OSRM (car, bike, foot). Xe máy ở VN gần
	// với "car" hơn "bike" vì vẫn đi đường lớn.
	Profile string
	// Fallback dùng khi OSRM lỗi. Không có nó thì OSRM chết là dispatch chết.
	Fallback ETAEngine
	// CacheTTL là hạn của một ô nhớ đệm. Ngắn thôi: tình hình giao thông đổi.
	CacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]etaCacheEntry
}

type etaCacheEntry struct {
	sec float64
	at  time.Time
}

func NewOSRMETA(baseURL string, fallback ETAEngine) *OSRMETA {
	return &OSRMETA{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 3 * time.Second},
		Profile:  "car",
		Fallback: fallback,
		CacheTTL: 60 * time.Second,
		cache:    map[string]etaCacheEntry{},
	}
}

// cacheKey gộp theo cặp ô lưới, không theo toạ độ chính xác.
func cacheKey(from, to geo.Point) string {
	return geo.CellOf(from).Key() + "|" + geo.CellOf(to).Key()
}

func (o *OSRMETA) ETASeconds(ctx context.Context, from []geo.Point, to geo.Point) ([]float64, error) {
	out := make([]float64, len(from))
	for i := range out {
		out[i] = -1
	}

	// Lấy những gì cache đã có; chỉ hỏi OSRM phần còn thiếu.
	now := time.Now()
	var missIdx []int
	o.mu.Lock()
	for i, p := range from {
		if e, ok := o.cache[cacheKey(p, to)]; ok && now.Sub(e.at) < o.CacheTTL {
			out[i] = e.sec
			continue
		}
		missIdx = append(missIdx, i)
	}
	o.mu.Unlock()

	if len(missIdx) == 0 {
		return out, nil
	}

	srcs := make([]geo.Point, len(missIdx))
	for k, i := range missIdx {
		srcs[k] = from[i]
	}
	durs, err := o.table(ctx, srcs, to)
	if err != nil {
		if o.Fallback == nil {
			return nil, err
		}
		// OSRM chết không được làm chết dispatch. Haversine kém chính xác hơn
		// nhưng vẫn xếp hạng được ứng viên — thà ghép hơi lệch còn hơn không ghép.
		fb, ferr := o.Fallback.ETASeconds(ctx, srcs, to)
		if ferr != nil {
			return nil, err
		}
		for k, i := range missIdx {
			out[i] = fb[k]
		}
		return out, nil
	}

	o.mu.Lock()
	for k, i := range missIdx {
		out[i] = durs[k]
		if durs[k] >= 0 {
			o.cache[cacheKey(from[i], to)] = etaCacheEntry{sec: durs[k], at: now}
		}
	}
	// Cache không bao giờ dọn sẽ phình theo số cặp ô lưới đã từng gặp.
	if len(o.cache) > 50000 {
		for k, e := range o.cache {
			if now.Sub(e.at) >= o.CacheTTL {
				delete(o.cache, k)
			}
		}
	}
	o.mu.Unlock()
	return out, nil
}

// table gọi OSRM /table với nhiều nguồn và MỘT đích.
//
// `sources` là các tài xế, `destinations` là điểm đón — nhờ vậy ma trận chỉ có
// một cột thay vì N×N.
func (o *OSRMETA) table(ctx context.Context, from []geo.Point, to geo.Point) ([]float64, error) {
	var sb strings.Builder
	for _, p := range from {
		fmt.Fprintf(&sb, "%.6f,%.6f;", p.Lng, p.Lat)
	}
	fmt.Fprintf(&sb, "%.6f,%.6f", to.Lng, to.Lat)

	srcIdx := make([]string, len(from))
	for i := range from {
		srcIdx[i] = fmt.Sprint(i)
	}
	url := fmt.Sprintf("%s/table/v1/%s/%s?sources=%s&destinations=%d",
		o.baseURL, o.Profile, sb.String(), strings.Join(srcIdx, ";"), len(from))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "osrm_request_failed", "osrm", err)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "osrm_unreachable", "osrm", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errs.E(errs.KindInternal, "osrm_bad_status",
			fmt.Sprintf("OSRM trả %d", resp.StatusCode))
	}

	var body struct {
		Code      string      `json:"code"`
		Durations [][]float64 `json:"durations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "osrm_decode_failed", "osrm", err)
	}
	if body.Code != "Ok" {
		return nil, errs.E(errs.KindInternal, "osrm_error", "OSRM trả mã "+body.Code)
	}
	if len(body.Durations) != len(from) {
		return nil, errs.E(errs.KindInternal, "osrm_shape_mismatch",
			"OSRM trả ma trận sai kích thước")
	}

	out := make([]float64, len(from))
	for i, row := range body.Durations {
		// Mỗi hàng đúng một cột vì chỉ có một điểm đến. null = không có đường đi.
		if len(row) == 0 {
			out[i] = -1
			continue
		}
		out[i] = row[0]
	}
	return out, nil
}
