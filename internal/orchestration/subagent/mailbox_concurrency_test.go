package subagent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMailboxSerializesPersistenceInSequenceOrder(t *testing.T) {
	mailbox := NewMailbox()
	firstPersisting := make(chan struct{})
	secondPersisting := make(chan struct{})
	releaseFirst := make(chan struct{})
	mailbox.persist = func(message Message) error {
		switch message.Sequence {
		case 1:
			close(firstPersisting)
			<-releaseFirst
		case 2:
			close(secondPersisting)
		}
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := mailbox.Enqueue(Message{
			To: "agent", Body: json.RawMessage(`{"sequence":1}`),
		})
		firstDone <- err
	}()
	<-firstPersisting
	secondDone := make(chan error, 1)
	go func() {
		_, err := mailbox.Enqueue(Message{
			To: "agent", Body: json.RawMessage(`{"sequence":2}`),
		})
		secondDone <- err
	}()
	select {
	case <-secondPersisting:
		t.Fatal("second message persisted before the first committed")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	messages := mailbox.Pending("agent")
	if len(messages) != 2 ||
		messages[0].Sequence != 1 ||
		messages[1].Sequence != 2 {
		t.Fatalf("pending messages = %+v", messages)
	}
}

func TestMailboxCloseWaitsForPersistenceCommit(t *testing.T) {
	mailbox := NewMailbox()
	persisting := make(chan struct{})
	release := make(chan struct{})
	mailbox.persist = func(Message) error {
		close(persisting)
		<-release
		return nil
	}
	enqueued := make(chan error, 1)
	go func() {
		_, err := mailbox.Enqueue(Message{
			To: "agent", Body: json.RawMessage(`{"message":"before-close"}`),
		})
		enqueued <- err
	}()
	<-persisting
	closed := make(chan struct{})
	go func() {
		mailbox.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while persistence was in flight")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-enqueued; err != nil {
		t.Fatal(err)
	}
	<-closed
	if messages := mailbox.Pending("agent"); len(messages) != 1 {
		t.Fatalf("pending messages after Close = %d", len(messages))
	}
	if _, err := mailbox.Enqueue(Message{
		To: "agent", Body: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("enqueue succeeded after Close")
	}
}

func TestMailboxDrainReturnsMessagesWhenAckFails(t *testing.T) {
	mailbox := NewMailbox()
	mailbox.deliver = func(Message) error {
		return errors.New("delivery persist failed")
	}
	if _, err := mailbox.Deliver("agent-1", SessionParentID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	got := mailbox.Drain(SessionParentID)
	if len(got) != 1 || got[0].Kind != MessageContext {
		t.Fatalf("drain = %+v", got)
	}
	if pending := mailbox.Pending(SessionParentID); len(pending) != 1 {
		t.Fatalf("pending after failed ack = %+v", pending)
	}
}
