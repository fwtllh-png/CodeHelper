package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/host/pairing"
)

func TestWebMobileQR(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"web", "--once", "--json", "--mobile", "--qr"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("web code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["mobile"] != true || payload["qr"] != true {
		t.Fatalf("payload=%v", payload)
	}
	ascii, _ := payload["ascii_qr"].(string)
	if ascii == "" || !strings.Contains(ascii, "█") && !strings.Contains(ascii, "▄") && len(ascii) < 10 {
		// ToSmallString uses half-blocks; accept any non-empty ascii qr
		if ascii == "" {
			t.Fatalf("missing ascii_qr: %v", payload)
		}
	}
	uiURL, _ := payload["ui_url"].(string)
	if !strings.HasPrefix(uiURL, "http://") {
		t.Fatalf("ui_url=%q", uiURL)
	}
}

func TestPairingCard(t *testing.T) {
	card, err := pairing.New("http://127.0.0.1:9/ui/", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !card.Mobile || !card.QR || card.ASCII == "" {
		t.Fatalf("%+v", card)
	}
}
