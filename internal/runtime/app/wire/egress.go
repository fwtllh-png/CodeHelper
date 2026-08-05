package wire

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

func grantRouteHosts(gate *egress.Gate, routes model.RouteSet) {
	if gate == nil || !routes.Ready() {
		return
	}
	gate.AllowURL(routes.Act().Endpoint())
	for _, purpose := range routes.Slots() {
		route, err := routes.For(purpose)
		if err != nil {
			continue
		}
		gate.AllowURL(route.Endpoint())
	}
}

func grantWebBackendHosts(gate *egress.Gate, options webtool.Options) {
	if gate == nil {
		return
	}
	for _, raw := range []string{
		options.PrimaryURL, options.FallbackURL, options.TavilyURL,
		options.SearXNGURL, options.BochaURL, options.SearchURL,
	} {
		gate.AllowURL(raw)
	}
}
