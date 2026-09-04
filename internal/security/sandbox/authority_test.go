package sandbox

import "testing"

func TestLoopbackOnlyRejectsProxyOrOutboundTargets(t *testing.T) {
	loopback := ExecutionAuthority{
		AllowLoopback:  true,
		NetworkTargets: []string{"loopback://localhost:0"},
	}
	if !loopback.LoopbackOnly() {
		t.Fatal("loopback-only authority was rejected")
	}
	withProxy := loopback
	withProxy.ManagedProxyPort = 43128
	if withProxy.LoopbackOnly() {
		t.Fatal("loopback authority with a managed proxy port is not loopback-only")
	}
	withOutbound := loopback
	withOutbound.NetworkTargets = []string{
		"loopback://localhost:0", "https://example.com:443",
	}
	if withOutbound.LoopbackOnly() {
		t.Fatal("loopback authority with outbound targets is not loopback-only")
	}
}
