package tui

import (
	"strings"
)

// streamMD partitions streaming assistant markdown into a stable (glamour-cached)
// prefix and a mutable tail. Table holdback keeps incomplete pipe tables in the
// tail until a blank line closes them or finalize runs.
type streamMD struct {
	source       string
	stableSrcLen int // bytes already rendered into stableANSI
	queuedSrcLen int // bytes accounted for (stable + commitQueue)
	stableANSI   string
	width        int
	commitQueue  []string // MotionFull: source chunks waiting to drip
}

func (s *streamMD) ensureWidth(width int) {
	if width < 24 {
		width = 24
	}
	if s.width == width {
		return
	}
	s.width = width
	if s.stableSrcLen > 0 {
		s.rebuildStableANSI()
	}
}

func (s *streamMD) pushDelta(delta string, motion MotionMode, width int) {
	if delta == "" {
		return
	}
	s.ensureWidth(width)
	s.source += delta
	s.advanceCommit(motion)
}

func (s *streamMD) advanceCommit(motion MotionMode) {
	boundary := s.commitBoundary()
	if boundary <= s.queuedSrcLen {
		return
	}
	chunk := s.source[s.queuedSrcLen:boundary]
	s.queuedSrcLen = boundary
	if motion == MotionFull {
		s.enqueueLines(chunk)
		return
	}
	s.appendStableChunk(chunk)
}

func (s *streamMD) enqueueLines(chunk string) {
	for chunk != "" {
		idx := strings.IndexByte(chunk, '\n')
		if idx < 0 {
			s.commitQueue = append(s.commitQueue, chunk)
			return
		}
		s.commitQueue = append(s.commitQueue, chunk[:idx+1])
		chunk = chunk[idx+1:]
	}
}

func (s *streamMD) appendStableChunk(chunk string) {
	if chunk == "" {
		return
	}
	s.stableSrcLen += len(chunk)
	s.rebuildStableANSI()
}

// drip commits one queued line into stableANSI. Returns true if work was done.
func (s *streamMD) drip() bool {
	if len(s.commitQueue) == 0 {
		return false
	}
	chunk := s.commitQueue[0]
	s.commitQueue = s.commitQueue[1:]
	s.appendStableChunk(chunk)
	return true
}

func (s *streamMD) hasPendingCommit() bool {
	return len(s.commitQueue) > 0
}

func (s *streamMD) flushQueue() {
	for len(s.commitQueue) > 0 {
		s.drip()
	}
}

// commitBoundary returns the byte offset up to which source may leave the
// mutable tail (complete lines, minus any open pipe-table holdback).
func (s *streamMD) commitBoundary() int {
	lastNL := strings.LastIndexByte(s.source, '\n')
	if lastNL < 0 {
		return 0
	}
	complete := lastNL + 1
	if hold := tableHoldbackStart(s.source[:complete]); hold >= 0 && hold < complete {
		return hold
	}
	return complete
}

func (s *streamMD) rebuildStableANSI() {
	src := s.source[:s.stableSrcLen]
	if strings.TrimSpace(src) == "" {
		s.stableANSI = ""
		return
	}
	rendered, err := renderMarkdown(src, s.width)
	if err != nil || rendered == "" {
		s.stableANSI = styleAsst.Render(src)
		return
	}
	s.stableANSI = rendered
}

func (s *streamMD) display() string {
	label := styleMuted.Render("assistant")
	tail := s.source[s.stableSrcLen:]
	var body string
	switch {
	case s.stableANSI != "" && tail != "":
		body = s.stableANSI + "\n" + styleAsst.Render(tail)
	case s.stableANSI != "":
		body = s.stableANSI
	case tail != "":
		body = styleAsst.Render(tail)
	default:
		return label
	}
	return label + "\n" + body
}

// tableHoldbackStart returns the byte offset of an open pipe table's header
// within completeSrc (which ends on a newline boundary), or -1 if none.
func tableHoldbackStart(completeSrc string) int {
	if completeSrc == "" {
		return -1
	}
	lines := strings.Split(strings.TrimSuffix(completeSrc, "\n"), "\n")
	offsets := lineByteOffsets(completeSrc, lines)

	inFence := false
	openTable := -1
	i := 0
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			openTable = -1
			i++
			continue
		}
		if inFence {
			i++
			continue
		}
		if openTable >= 0 {
			if trim == "" {
				openTable = -1
				i++
				continue
			}
			if isTableRow(line) && !isTableSeparator(lines, i) {
				i++
				continue
			}
			// Non-table content closes the table.
			openTable = -1
			continue
		}
		if isTableRow(line) && i+1 < len(lines) && isTableSeparator(lines, i+1) {
			openTable = offsets[i]
			i += 2
			continue
		}
		i++
	}
	return openTable
}

func lineByteOffsets(src string, lines []string) []int {
	offsets := make([]int, len(lines))
	pos := 0
	for i, line := range lines {
		offsets[i] = pos
		pos += len(line)
		if pos < len(src) && src[pos] == '\n' {
			pos++
		}
	}
	return offsets
}
