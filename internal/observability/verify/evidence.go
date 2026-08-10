package verify

const EvidenceMetadataKey = "verification_evidence"

// Evidence is a successful or failed structured quality command together with
// the exact workspace paths it claims to cover. The engine adds CallID and
// MutationRevision after execution; tool arguments cannot choose either value.
type Evidence struct {
	SchemaVersion    int      `json:"schema_version"`
	Kind             string   `json:"kind"`
	Status           string   `json:"status"`
	CoveredPaths     []string `json:"covered_paths"`
	CommandDigest    string   `json:"command_digest"`
	CallID           string   `json:"call_id,omitempty"`
	ExitCode         int      `json:"exit_code"`
	MutationRevision uint64   `json:"mutation_revision,omitempty"`
}
