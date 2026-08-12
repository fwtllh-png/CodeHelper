package fixture

import (
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// SliceStream is a deterministic provider stream for tests.
type SliceStream struct {
	Events []provider.StreamEvent
	index  int
}

func (s *SliceStream) Recv() (provider.StreamEvent, error) {
	if s.index >= len(s.Events) {
		return provider.StreamEvent{}, io.EOF
	}
	event := s.Events[s.index]
	s.index++
	return event, nil
}

func (s *SliceStream) Close() error {
	s.index = len(s.Events)
	return nil
}
