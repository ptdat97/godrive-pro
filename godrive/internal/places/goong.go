package places

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// GoongBaseURL là gốc REST API của Goong.
const GoongBaseURL = "https://rsapi.goong.io"

// Goong gọi REST API của Goong. Định dạng phản hồi tương thích Google Places
// (predictions / result / results, trường status).
//
// Viết theo tài liệu công khai, CHƯA chạy với key thật — xem docs/08 §8.15:
// lần đầu bật phải chạy TestGoongLive để xác nhận.
type Goong struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewGoong(baseURL, apiKey string) *Goong {
	return &Goong{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		// Tìm địa điểm nằm trên đường gõ phím của người dùng: chậm hơn 3 giây
		// thì kết quả cũng không còn ai cần.
		http: &http.Client{Timeout: 3 * time.Second},
	}
}

type goongLoc struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (g *Goong) get(ctx context.Context, path string, q url.Values, out any) error {
	q.Set("api_key", g.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "places_request_failed", "goong", err)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "places_unreachable", "goong", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// 401/403 = sai key hoặc hết hạn mức. Không lộ chi tiết ra cho app,
		// nhưng mã lỗi nói đủ để vận hành tra.
		return errs.E(errs.KindInternal, "places_bad_status", fmt.Sprintf("Goong trả %d", resp.StatusCode))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return errs.Wrap(errs.KindInternal, "places_decode_failed", "goong", err)
	}
	return nil
}

// checkStatus: "OK" là có kết quả, "ZERO_RESULTS" là không có gì — cả hai đều
// không phải lỗi. Mọi trạng thái khác là lỗi của phía nhà cung cấp.
func checkStatus(s string) error {
	if s == "" || s == "OK" || s == "ZERO_RESULTS" {
		return nil
	}
	return errs.E(errs.KindInternal, "places_provider_error", "Goong: "+s)
}

func (g *Goong) Autocomplete(ctx context.Context, input string, near *geo.Point) ([]Suggestion, error) {
	q := url.Values{"input": {input}, "limit": {"8"}}
	if near != nil {
		q.Set("location", fmt.Sprintf("%.6f,%.6f", near.Lat, near.Lng))
	}
	var body struct {
		Status      string `json:"status"`
		Predictions []struct {
			PlaceID     string `json:"place_id"`
			Description string `json:"description"`
			Structured  struct {
				Main      string `json:"main_text"`
				Secondary string `json:"secondary_text"`
			} `json:"structured_formatting"`
		} `json:"predictions"`
	}
	if err := g.get(ctx, "/Place/AutoComplete", q, &body); err != nil {
		return nil, err
	}
	if err := checkStatus(body.Status); err != nil {
		return nil, err
	}
	out := make([]Suggestion, 0, len(body.Predictions))
	for _, p := range body.Predictions {
		s := Suggestion{PlaceID: p.PlaceID, Title: p.Structured.Main, Address: p.Structured.Secondary}
		if s.Title == "" {
			s.Title = p.Description
		}
		out = append(out, s)
	}
	return out, nil
}

func (g *Goong) Detail(ctx context.Context, placeID string) (Place, error) {
	var body struct {
		Status string `json:"status"`
		Result struct {
			PlaceID          string `json:"place_id"`
			Name             string `json:"name"`
			FormattedAddress string `json:"formatted_address"`
			Geometry         struct {
				Location goongLoc `json:"location"`
			} `json:"geometry"`
		} `json:"result"`
	}
	if err := g.get(ctx, "/Place/Detail", url.Values{"place_id": {placeID}}, &body); err != nil {
		return Place{}, err
	}
	if err := checkStatus(body.Status); err != nil {
		return Place{}, err
	}
	r := body.Result
	p := Place{PlaceID: r.PlaceID, Name: r.Name, Address: r.FormattedAddress,
		Point: geo.Point{Lat: r.Geometry.Location.Lat, Lng: r.Geometry.Location.Lng}}
	if !p.Point.Valid() || (p.Point.Lat == 0 && p.Point.Lng == 0) {
		return Place{}, errs.NotFound("place_not_found", "Không tìm thấy địa điểm.")
	}
	if p.PlaceID == "" {
		p.PlaceID = placeID
	}
	return p, nil
}

func (g *Goong) Reverse(ctx context.Context, pt geo.Point) (Place, error) {
	var body struct {
		Status  string `json:"status"`
		Results []struct {
			PlaceID          string `json:"place_id"`
			Name             string `json:"name"`
			FormattedAddress string `json:"formatted_address"`
		} `json:"results"`
	}
	q := url.Values{"latlng": {fmt.Sprintf("%.6f,%.6f", pt.Lat, pt.Lng)}}
	if err := g.get(ctx, "/Geocode", q, &body); err != nil {
		return Place{}, err
	}
	if err := checkStatus(body.Status); err != nil {
		return Place{}, err
	}
	if len(body.Results) == 0 {
		return Place{}, errs.NotFound("place_not_found", "Không tìm được địa chỉ cho vị trí này.")
	}
	r := body.Results[0]
	name := r.Name
	if name == "" {
		// Không có tên riêng thì lấy phần đầu địa chỉ ("12 Lê Lợi") làm tên.
		name, _, _ = strings.Cut(r.FormattedAddress, ",")
	}
	return Place{PlaceID: r.PlaceID, Name: name, Address: r.FormattedAddress, Point: pt}, nil
}
