package openaicompat

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
)

func NewAdapter() (*openai.Adapter, error) {
	return openai.NewAdapter(model.AdapterOpenAICompatible)
}
