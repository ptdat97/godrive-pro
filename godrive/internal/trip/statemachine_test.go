package trip

import "testing"

func TestTransitionGraph(t *testing.T) {
	ok := [][2]Status{
		{StatusCreated, StatusSearching},
		{StatusSearching, StatusAssigned},
		{StatusAssigned, StatusArrived},
		{StatusArrived, StatusInProgress},
		{StatusInProgress, StatusCompleted},
		{StatusCompleted, StatusPaid},
		{StatusSearching, StatusExpired},
	}
	for _, c := range ok {
		if !CanTransition(c[0], c[1]) {
			t.Fatalf("phải cho phép %s -> %s", c[0], c[1])
		}
	}
	bad := [][2]Status{
		{StatusCreated, StatusInProgress},
		{StatusInProgress, StatusCancelled}, // đang chở khách thì không huỷ
		{StatusPaid, StatusCancelled},
		{StatusCancelled, StatusSearching},
		{StatusCompleted, StatusInProgress},
	}
	for _, c := range bad {
		if CanTransition(c[0], c[1]) {
			t.Fatalf("phải chặn %s -> %s", c[0], c[1])
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []Status{StatusPaid, StatusCancelled, StatusExpired} {
		if !s.IsTerminal() {
			t.Fatalf("%s phải là trạng thái cuối", s)
		}
	}
	if StatusSearching.IsTerminal() {
		t.Fatal("SEARCHING không phải trạng thái cuối")
	}
}
