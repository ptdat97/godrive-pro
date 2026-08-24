package money

import "testing"

func TestFormat(t *testing.T) {
	cases := map[VND]string{
		0:       "0\u20AB",
		1250000: "1.250.000\u20AB",
		-50000:  "-50.000\u20AB",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Fatalf("String(%d)=%q want %q", in, got, want)
		}
	}
}

func TestPermilleAndRound(t *testing.T) {
	if got := VND(50000).MulPermille(200); got != 10000 {
		t.Fatalf("chiết khấu 20%% của 50000 = %d", got)
	}
	if got := VND(23400).RoundTo(1000); got != 24000 {
		t.Fatalf("RoundTo = %d", got)
	}
}
