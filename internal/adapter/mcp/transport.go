package mcp

import (
	"context"
	"encoding/json"
)

type Transport interface {
	Request(context.Context, string, any, any) error
	Notify(context.Context, string, any) error
	Close(context.Context) error
	StderrTail() string
}

type TransportFactory func(context.Context, string, ServerConfig) (Transport, error)

type Notification struct {
	Method string
	Params json.RawMessage
}

type NotificationSource interface {
	SetNotificationHandler(func(Notification))
}

type FailureSource interface {
	SetFailureHandler(func(error))
}

func decodeResult(response Response, target any) error {
	if response.Error != nil {
		return response.Error
	}
	if target == nil {
		return nil
	}
	if len(response.Result) == 0 {
		return json.Unmarshal([]byte("null"), target)
	}
	return DecodeStrict(response.Result, target)
}
