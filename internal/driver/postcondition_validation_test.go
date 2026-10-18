package driver

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
)

func TestInvalidPostconditionsNeverDispatchInput(t *testing.T) {
	for _, c := range []model.Postcondition{
		{Kind: "property", Element: "field", Property: "value", Expected: "4", MaxWaitMillis: 0},
		{Kind: "property", Element: "field", Property: "value", Expected: "4", MaxWaitMillis: 2001},
		{Kind: "shell", Expected: "4", MaxWaitMillis: 10},
		{Kind: "property", Element: "field", Property: "password", Expected: "4", MaxWaitMillis: 10},
	} {
		a, v := fixtureAction()
		a.Postconditions = []model.Postcondition{c}
		f := &fakeCaller{}
		d := &Cua{client: f}
		if got := d.Execute(context.Background(), a, v, "background"); got.State != "failed" || len(f.calls) != 0 {
			t.Fatalf("invalid condition dispatched: %+v %v", got, f.calls)
		}
	}
}
