package pkg

import "testing"

func TestAward(t *testing.T) {
	if Award(1) != 1+Bonus {
		t.Fatal("Award ignored the bonus")
	}
}
