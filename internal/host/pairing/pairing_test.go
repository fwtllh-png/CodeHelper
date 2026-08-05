package pairing_test

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/pairing"
)

func TestPairingCard(t *testing.T) {
	card, err := pairing.New("http://127.0.0.1:9/ui/", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !card.Mobile || !card.QR || card.ASCII == "" || card.URL == "" {
		t.Fatalf("%+v", card)
	}
}
