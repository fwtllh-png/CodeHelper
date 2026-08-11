package interact

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestC5HostRestoresInputWaitWithoutDuplicateEmission(t *testing.T) {
	host := NewHost(time.Minute)
	var emissions atomic.Int32
	host.SetEmitter(func(context.Context, Request) error {
		emissions.Add(1)
		return nil
	})
	request := Request{
		RequestID: "input-restored",
		CallID:    "call-restored",
		Tool:      "request_user_input",
		Prompt:    "continue?",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := host.RestoreRequest(request); err != nil {
		t.Fatal(err)
	}
	result := make(chan Reply, 1)
	errs := make(chan error, 1)
	go func() {
		reply, err := host.Wait(
			t.Context(),
			request.CallID,
			request.Prompt,
			nil,
		)
		if err != nil {
			errs <- err
			return
		}
		result <- reply
	}()
	deadline := time.Now().Add(time.Second)
	for {
		err := host.StageReply(Reply{
			RequestID: request.RequestID,
			Answer:    "yes",
		})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := host.Resume(request.RequestID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case reply := <-result:
		if reply.RequestID != request.RequestID ||
			reply.Answer != "yes" ||
			emissions.Load() != 0 {
			t.Fatalf(
				"reply=%+v emissions=%d",
				reply,
				emissions.Load(),
			)
		}
	case <-time.After(time.Second):
		t.Fatal("restored input wait did not resume")
	}
}
