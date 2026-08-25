package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/example/godrive/pkg/errs"
	"github.com/example/godrive/pkg/geo"
)

// OSRMEngine tính quãng đường và thời lượng thật bằng OSRM tự host.
//
// HaversineEngine nhân hệ số uốn khúc 1.35 là ước lượng thô: nó không biết
// sông, đường một chiều hay cầu. Sai số đó đi thẳng vào giá cước khách trả.
type OSRMEngine struct {
	baseURL string
	http    *http.Client
	Profile string
	// Fallback dùng khi OSRM lỗi. Báo giá là đường đi của tiền và nằm trên
	// request path — thà ước lượng thô còn hơn không báo được giá.
	Fallback RouteEngine
}

func NewOSRMEngine(baseURL string, fallback RouteEngine) *OSRMEngine {
	return &OSRMEngine{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: 3 * time.Second},
		Profile:  "car",
		Fallback: fallback,
	}
}

func (o *OSRMEngine) Route(ctx context.Context, from, to geo.Point) (Route, error) {
	r, err := o.route(ctx, from, to)
	if err == nil {
		return r, nil
	}
	if o.Fallback == nil {
		return Route{}, err
	}
	return o.Fallback.Route(ctx, from, to)
}

func (o *OSRMEngine) route(ctx context.Context, from, to geo.Point) (Route, error) {
	url := fmt.Sprintf("%s/route/v1/%s/%.6f,%.6f;%.6f,%.6f?overview=false",
		o.baseURL, o.Profile, from.Lng, from.Lat, to.Lng, to.Lat)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Route{}, errs.Wrap(errs.KindInternal, "osrm_request_failed", "osrm", err)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return Route{}, errs.Wrap(errs.KindInternal, "osrm_unreachable", "osrm", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Route{}, errs.E(errs.KindInternal, "osrm_bad_status",
			fmt.Sprintf("OSRM trả %d", resp.StatusCode))
	}

	var body struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Geometry string  `json:"geometry"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Route{}, errs.Wrap(errs.KindInternal, "osrm_decode_failed", "osrm", err)
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		return Route{}, errs.E(errs.KindInternal, "osrm_no_route",
			"Không tìm được đường đi giữa hai điểm.")
	}
	return Route{
		DistanceM: body.Routes[0].Distance,
		DurationS: body.Routes[0].Duration,
		Polyline:  body.Routes[0].Geometry,
	}, nil
}
