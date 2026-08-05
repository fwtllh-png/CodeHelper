// Package pairing emits mobile pairing payloads and ASCII QR codes for UI URLs.
package pairing

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Card describes a mobile/QR pairing target for serve/web hosts.
type Card struct {
	Mobile bool   `json:"mobile"`
	QR     bool   `json:"qr"`
	URL    string `json:"url"`
	ASCII  string `json:"ascii_qr,omitempty"`
	Hint   string `json:"hint"`
}

// New builds a pairing card for the given UI URL.
func New(url string, wantMobile, wantQR bool) (Card, error) {
	card := Card{
		Mobile: wantMobile || wantQR,
		QR:     wantQR,
		URL:    url,
		Hint:   "open ui_url on a phone browser on the same network",
	}
	if !wantQR {
		return card, nil
	}
	code, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return Card{}, fmt.Errorf("encode qr: %w", err)
	}
	card.ASCII = strings.TrimRight(code.ToSmallString(false), "\n")
	card.Hint = "scan ascii_qr or open ui_url on a phone browser"
	return card, nil
}
