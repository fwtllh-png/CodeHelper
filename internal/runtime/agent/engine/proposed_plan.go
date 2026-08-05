package engine

import "strings"

const (
	proposedPlanOpen  = "<proposed_plan>"
	proposedPlanClose = "</proposed_plan>"
)

// ProposedPlanUpdate is emitted while the model streams a proposed_plan block.
type ProposedPlanUpdate struct {
	Delta string `json:"delta,omitempty"`
	Body  string `json:"body,omitempty"`
	Done  bool   `json:"done,omitempty"`
}

// ProposedPlanParser extracts <proposed_plan>…</proposed_plan> from streamed text.
type ProposedPlanParser struct {
	buf  strings.Builder
	body strings.Builder
	open bool
}

// Feed consumes a text chunk and returns zero or more plan updates.
func (p *ProposedPlanParser) Feed(chunk string) []ProposedPlanUpdate {
	if chunk == "" {
		return nil
	}
	p.buf.WriteString(chunk)
	var out []ProposedPlanUpdate
	for {
		text := p.buf.String()
		if !p.open {
			index := strings.Index(text, proposedPlanOpen)
			if index < 0 {
				// Keep a short suffix that might be a partial open tag.
				keep := partialSuffix(text, proposedPlanOpen)
				p.buf.Reset()
				p.buf.WriteString(keep)
				return out
			}
			rest := text[index+len(proposedPlanOpen):]
			p.buf.Reset()
			p.buf.WriteString(rest)
			p.open = true
			p.body.Reset()
			continue
		}
		index := strings.Index(text, proposedPlanClose)
		if index < 0 {
			keep := partialSuffix(text, proposedPlanClose)
			emit := strings.TrimSuffix(text, keep)
			if emit != "" {
				p.body.WriteString(emit)
				out = append(out, ProposedPlanUpdate{Delta: emit, Body: p.body.String()})
			}
			p.buf.Reset()
			p.buf.WriteString(keep)
			return out
		}
		emit := text[:index]
		if emit != "" {
			p.body.WriteString(emit)
			out = append(out, ProposedPlanUpdate{Delta: emit, Body: p.body.String()})
		}
		// Trim surrounding whitespace on close so PlanCard shows clean markdown.
		out = append(out, ProposedPlanUpdate{Body: strings.TrimSpace(p.body.String()), Done: true})
		p.open = false
		p.body.Reset()
		p.buf.Reset()
		p.buf.WriteString(text[index+len(proposedPlanClose):])
	}
}

func partialSuffix(text, marker string) string {
	max := len(marker) - 1
	if max > len(text) {
		max = len(text)
	}
	for size := max; size > 0; size-- {
		if strings.HasPrefix(marker, text[len(text)-size:]) {
			return text[len(text)-size:]
		}
	}
	return ""
}
