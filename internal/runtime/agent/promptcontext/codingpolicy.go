package promptcontext

// PartitionCodingPolicy is the working method the agent is asked to follow. It
// belongs to the stable prefix: the text never changes within a session, so it
// costs cache nothing, and the volatile evidence section at the tail is what
// reports how well the method is being followed.
const PartitionCodingPolicy = "coding_policy"

// codingPolicy is the method, in the order the steps are taken.
//
// Two of the rules are enforced by the runtime rather than by the model reading
// them, and they say so: a model that knows a rule is mechanical stops spending
// reasoning on it and starts trusting the error message when it trips.
const codingPolicy = `Coding method:
- Start from the repository conventions and build files named above; do not
  rediscover them by listing directories.
- Sort what you find: what declares a symbol, what uses it, what tests it, what
  configures it. search_definition, search_references and search_related_tests
  answer those directly.
- Read a file before editing it. Enforced: an unread or stale file fails the
  write, and the error says what to re-read.
- After editing, verify the affected scope first and widen only if it passes. An
  unverified change is reported back to you as an open risk.
- Do not repeat a search or a read you already have; a repeated call is reported
  back to you.`

// CodingPolicySection carries the method as a diffable partition.
type CodingPolicySection struct {
	Body string `json:"body"`
}

// NewCodingPolicySection returns the built-in method.
func NewCodingPolicySection() CodingPolicySection {
	return CodingPolicySection{Body: codingPolicy}
}

func (c CodingPolicySection) ID() string { return PartitionCodingPolicy }

func (c CodingPolicySection) Digest() string { return digestJSON(c) }

func (c CodingPolicySection) Render() string { return c.Body }
