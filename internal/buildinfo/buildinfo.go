package buildinfo

import (
	"runtime"

	"github.com/fwtllh-png/CodeHelper/internal/compatibility"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Name                   string `json:"name"`
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	BuildDate              string `json:"build_date"`
	GoVersion              string `json:"go_version"`
	OS                     string `json:"os"`
	Arch                   string `json:"arch"`
	ACPProtocolMin         int    `json:"acp_protocol_min"`
	ACPProtocolMax         int    `json:"acp_protocol_max"`
	OperationSchemaVersion int    `json:"operation_schema_version"`
}

func Current() Info {
	manifest := compatibility.MustLoad()
	return Info{
		Name:                   "codehelper",
		Version:                Version,
		Commit:                 Commit,
		BuildDate:              Date,
		GoVersion:              runtime.Version(),
		OS:                     runtime.GOOS,
		Arch:                   runtime.GOARCH,
		ACPProtocolMin:         manifest.ACPProtocol.Min,
		ACPProtocolMax:         manifest.ACPProtocol.Max,
		OperationSchemaVersion: manifest.OperationSchemaVersion,
	}
}
