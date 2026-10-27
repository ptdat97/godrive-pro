package main

import (
	"reflect"

	"github.com/example/godrive/internal/admin"
	"github.com/example/godrive/internal/driver"
	"github.com/example/godrive/internal/identity"
	"github.com/example/godrive/internal/location"
	"github.com/example/godrive/internal/platform/authn"
	"github.com/example/godrive/internal/platform/httpx"
	"github.com/example/godrive/internal/settings"
	"github.com/example/godrive/internal/trip"
	"github.com/example/godrive/pkg/geo"
)

// named là một kiểu Go được xuất sang TS dưới một tên cụ thể. Tên TS khác tên
// Go ở vài chỗ vì giao diện đã quen dùng tên đó (location.Snapshot là
// "LiveDriver" trên bản đồ).
type named struct {
	ts string
	t  reflect.Type
}

func tOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// enums: kiểu chuỗi có tập giá trị cố định. Giá trị lấy từ khai báo const
// trong mã nguồn — thêm một hằng ở Go là TS tự có, không ai phải nhớ chép.
var enums = []named{
	{"DriverStatus", tOf[driver.Status]()},
	{"KYCState", tOf[driver.KYCState]()},
	{"VehicleType", tOf[driver.VehicleType]()},
	{"DocumentKind", tOf[driver.DocumentKind]()},
	{"DocumentAlertLevel", tOf[driver.DocumentAlertLevel]()},
	{"TripStatus", tOf[trip.Status]()},
	{"PaymentMethod", tOf[trip.PaymentMethod]()},
	{"CancelBy", tOf[trip.CancelBy]()},
	{"AlertLevel", tOf[admin.AlertLevel]()},
	{"Role", tOf[authn.Role]()},
	{"SettingKind", tOf[settings.Kind]()},
	{"SettingKey", tOf[settings.Key]()},
}

// roots: những kiểu giao diện dùng, theo thứ tự xuất hiện trong tệp sinh ra.
// Kiểu struct có tên mà roots tham chiếu tới nhưng không liệt kê ở đây sẽ tự
// được xuất kèm, dưới tên Go của nó.
var roots = []named{
	{"Point", tOf[geo.Point]()},
	{"DriverRow", tOf[admin.DriverRow]()},
	{"DocumentExpiry", tOf[admin.DocumentExpiry]()},
	{"DocumentAlert", tOf[driver.DocumentAlert]()},
	{"TripRow", tOf[admin.TripRow]()},
	{"TripEvent", tOf[trip.Event]()},
	{"Alert", tOf[admin.Alert]()},
	{"Overview", tOf[admin.Overview]()},
	{"LiveDriver", tOf[location.Snapshot]()},
	{"PendingPickup", tOf[admin.PendingPickup]()},
	{"LiveMapResult", tOf[admin.LiveMapResult]()},
	{"AuditEntry", tOf[admin.AuditEntry]()},
	{"Account", tOf[identity.Account]()},
	{"TokenPair", tOf[identity.TokenPair]()},
	{"ApiError", tOf[httpx.ErrorBody]()},
	{"SettingField", tOf[settings.Field]()},
	{"SettingSection", tOf[settings.Section]()},
	{"SettingGroup", tOf[settings.GroupView]()},
	{"SettingHistoryEntry", tOf[settings.HistoryEntry]()},
}
