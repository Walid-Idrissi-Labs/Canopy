package core

import (
	"math"
	"testing"
)

// A rate that is not a number, or is no price anybody charges, is refused rather than stored.
func TestARateMustBeAPrice(t *testing.T) {
	for _, bad := range []KeyRate{
		{InputPerMTok: math.NaN(), OutputPerMTok: 1},
		{InputPerMTok: 1, OutputPerMTok: math.Inf(1)},
		{InputPerMTok: 1, OutputPerMTok: 2, CacheReadPerMTok: 1e300},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if err := (KeyRate{InputPerMTok: 0.6, OutputPerMTok: 2.5}).Validate(); err != nil {
		t.Fatal(err)
	}
}
