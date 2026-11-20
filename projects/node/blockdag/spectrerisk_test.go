package Blockdag

import (
	"testing"

	"gonum.org/v1/gonum/floats/scalar"
)

func TestOnlineRiskInSpectre(t *testing.T) {
	t.Parallel()
	if scalar.EqualWithinAbs(GetRisk(300, 0.1, 10, 5, 10, 30), 0.1509544, tol) {
		t.FailNow()
	}
}
