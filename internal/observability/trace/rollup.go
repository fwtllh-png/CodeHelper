package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Scope names which turns a Rollup covers. An empty field is not a filter, so a
// zero Scope means every turn in the database. Start is inclusive and End is
// exclusive, matching the usage queries.
type Scope struct {
	SessionID string
	ThreadID  string
	TurnID    string
	Start     time.Time
	End       time.Time
}

// Phase is one span name's share of a scope.
//
// TotalMS is a sum over spans, so phases do not partition the turn wall clock —
// the same caveat that applies to Latency applies here, and for the same reason:
// tools run in parallel.
type Phase struct {
	Name  string
	Calls uint64
	// Errors counts spans that ended in failure. A canceled span is not counted
	// here: a user who cancels has not hit a bug.
	Errors uint64
	// Unfinished counts spans nobody closed, which is what a crash leaves behind.
	// They are counted but contribute no time, because their duration is unknown
	// and filling in a zero would report them as instant.
	Unfinished uint64
	TotalMS    int64
	MaxMS      int64
}

// Rollup is how long a scope's turns took, broken down by phase.
type Rollup struct {
	Scope Scope
	// Turns counts distinct turns with at least one span.
	Turns  uint64
	Phases []Phase
	// TurnP50MS and TurnP95MS are percentiles over the turns' own root spans, so
	// they answer "how long does a turn take here" rather than "how long does a
	// phase take". They are zero when no turn in the scope finished.
	TurnP50MS int64
	TurnP95MS int64
}

// Empty reports that the scope has no recorded spans, which a caller should say
// out loud rather than render as a row of zeros.
func (r Rollup) Empty() bool {
	return r.Turns == 0 && len(r.Phases) == 0
}

// Phase returns the named phase and whether it was recorded at all.
func (r Rollup) Phase(name string) (Phase, bool) {
	for _, phase := range r.Phases {
		if phase.Name == name {
			return phase, true
		}
	}
	return Phase{}, false
}

// QueryRollup summarizes every span in the scope.
//
// The scope filter reaches session and thread through turns, because spans are
// keyed by turn alone: one trace is one turn, so a span has no identity above the
// turn that produced it.
func (r *Repository) QueryRollup(ctx context.Context, scope Scope) (Rollup, error) {
	if r.db == nil {
		return Rollup{}, errors.New("trace repository database is required")
	}
	if !scope.Start.IsZero() && !scope.End.IsZero() && !scope.Start.Before(scope.End) {
		return Rollup{}, errors.New("trace scope start must be before end")
	}
	query := `
		SELECT s.turn_id, s.name, s.duration_ms, s.status
		FROM spans s
		JOIN turns tr ON tr.id = s.turn_id
		JOIN threads th ON th.id = tr.thread_id
		WHERE 1 = 1`
	var arguments []any
	add := func(clause string, value any) {
		query += " AND " + clause
		arguments = append(arguments, value)
	}
	if scope.SessionID != "" {
		add("th.session_id = ?", scope.SessionID)
	}
	if scope.ThreadID != "" {
		add("tr.thread_id = ?", scope.ThreadID)
	}
	if scope.TurnID != "" {
		add("s.turn_id = ?", scope.TurnID)
	}
	if !scope.Start.IsZero() {
		add("s.started_at >= ?", timestamp(scope.Start))
	}
	if !scope.End.IsZero() {
		add("s.started_at < ?", timestamp(scope.End))
	}
	query += " ORDER BY tr.ordinal, s.span_id"
	rows, err := r.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return Rollup{}, fmt.Errorf("query span rollup: %w", err)
	}
	defer rows.Close()
	rollup := Rollup{Scope: scope}
	phases := make(map[string]*Phase)
	turns := make(map[string]struct{})
	var turnDurations []int64
	for rows.Next() {
		var turnID, name, status string
		var duration *int64
		if err := rows.Scan(&turnID, &name, &duration, &status); err != nil {
			return Rollup{}, err
		}
		turns[turnID] = struct{}{}
		phase, ok := phases[name]
		if !ok {
			phase = &Phase{Name: name}
			phases[name] = phase
		}
		phase.Calls++
		switch Status(status) {
		case StatusError:
			phase.Errors++
		case StatusOpen:
			phase.Unfinished++
		}
		if duration == nil {
			continue
		}
		phase.TotalMS += *duration
		if *duration > phase.MaxMS {
			phase.MaxMS = *duration
		}
		if name == NameTurn {
			turnDurations = append(turnDurations, *duration)
		}
	}
	if err := rows.Err(); err != nil {
		return Rollup{}, err
	}
	rollup.Turns = uint64(len(turns))
	rollup.Phases = sortPhases(phases)
	rollup.TurnP50MS = percentile(turnDurations, 50)
	rollup.TurnP95MS = percentile(turnDurations, 95)
	if rollup.Empty() {
		return r.queryMeasurementRollup(ctx, scope)
	}
	return rollup, nil
}

// QueryTurnInThread reads a turn's spans, refusing a turn that does not belong to
// the thread the caller named.
//
// The membership check is its own statement because a turn with no spans is a
// real answer — a turn can finish before anything worth tracing happened — and
// folding the two together would report it as a turn that does not exist.
func (r *Repository) QueryTurnInThread(
	ctx context.Context,
	threadID, turnID string,
) ([]Record, error) {
	if r.db == nil {
		return nil, errors.New("trace repository database is required")
	}
	var exists int
	err := r.db.QueryRowContext(ctx,
		"SELECT 1 FROM turns WHERE id = ? AND thread_id = ?", turnID, threadID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: turn %s in thread %s", ErrNotFound, turnID, threadID)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve trace turn: %w", err)
	}
	return r.QueryByTurn(ctx, turnID)
}

// sortPhases orders phases the way a turn runs so two reports of the same scope
// read the same, with names this package does not know about last.
func sortPhases(phases map[string]*Phase) []Phase {
	order := map[string]int{
		NameTurn: 0, NameModelCall: 1, NameTool: 2, NameApprovalWait: 3, NameVerify: 4,
	}
	rank := func(name string) int {
		if index, ok := order[name]; ok {
			return index
		}
		return len(order)
	}
	result := make([]Phase, 0, len(phases))
	for _, phase := range phases {
		result = append(result, *phase)
	}
	sort.Slice(result, func(i, j int) bool {
		if rank(result[i].Name) != rank(result[j].Name) {
			return rank(result[i].Name) < rank(result[j].Name)
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// percentile is nearest-rank over the samples, which needs no interpolation and
// therefore always returns a duration some turn actually took.
func percentile(samples []int64, share int) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]int64, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted)*share + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}
