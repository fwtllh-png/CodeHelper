package web

import (
	"bytes"
	"image/png"
	"os"
	"regexp"
	"testing"
)

func TestBrandAssetsShareGeometryAndExpectedSizes(t *testing.T) {
	component, err := os.ReadFile("src/ui/brand/CapybaraMark.tsx")
	if err != nil {
		t.Fatal(err)
	}
	favicon, err := os.ReadFile("public/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	pathPattern := regexp.MustCompile(`(?s)<path\b[^>]*\bd="([^"]+)"`)
	componentPath := pathPattern.FindSubmatch(component)
	faviconPath := pathPattern.FindSubmatch(favicon)
	if len(componentPath) != 2 || len(faviconPath) != 2 ||
		!bytes.Equal(componentPath[1], faviconPath[1]) {
		t.Fatal("React mark and favicon do not share the same vector geometry")
	}
	for _, fixture := range []struct {
		path string
		size int
	}{
		{path: "public/icon-192.png", size: 192},
		{path: "public/icon-512.png", size: 512},
	} {
		file, err := os.Open(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		config, err := png.DecodeConfig(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if config.Width != fixture.size || config.Height != fixture.size {
			t.Fatalf(
				"%s is %dx%d, want %dx%d",
				fixture.path,
				config.Width,
				config.Height,
				fixture.size,
				fixture.size,
			)
		}
	}
}
