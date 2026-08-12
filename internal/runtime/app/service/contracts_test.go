package service_test

import (
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/service"
)

var _ service.Session = (*app.SessionService)(nil)
var _ service.Artifact[app.TurnRecoveryPreparation, app.PlanTransitionPreparation] = (*app.ArtifactService)(nil)
