package wire

import apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"

type PersistentRuntimeOptions = apppersistence.PersistentRuntimeOptions

var NewPersistentRepositories = apppersistence.NewPersistentRepositories
var NewPersistentRuntime = apppersistence.NewPersistentRuntime
var EnsureThread = apppersistence.EnsureThread
