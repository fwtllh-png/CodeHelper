//go:build !webbundle

package web

import "testing"

func TestAssetsRequireRepositoryBuild(t *testing.T) {
	if _, err := Assets(); err == nil {
		t.Fatal("Assets() error = nil without webbundle build tag")
	}
}
