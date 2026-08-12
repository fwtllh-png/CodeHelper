package turnexec

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestMailboxIsBoundedAndFIFO(t *testing.T) {
	mailbox := NewMailbox[int](2)
	if err := mailbox.Offer(1); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Offer(2); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Offer(3); !protocol.IsCode(
		err,
		protocol.CodeResourceExhausted,
	) {
		t.Fatalf("full mailbox error = %v", err)
	}
	values := mailbox.Drain()
	if len(values) != 2 || values[0] != 1 || values[1] != 2 {
		t.Fatalf("drain = %v", values)
	}
}

func TestRequestLedgerRejectsDuplicateLateAndKindMismatch(t *testing.T) {
	ledger := NewRequestLedger()
	if err := ledger.Register(RequestApproval, "request-1"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Resolve(RequestInput, "request-1"); !protocol.IsCode(
		err,
		protocol.CodeConflict,
	) {
		t.Fatalf("kind mismatch = %v", err)
	}
	if err := ledger.Resolve(RequestApproval, "request-1"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Resolve(RequestApproval, "request-1"); !protocol.IsCode(
		err,
		protocol.CodeConflict,
	) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := ledger.Resolve(RequestInput, "request-2"); !protocol.IsCode(
		err,
		protocol.CodeConflict,
	) {
		t.Fatalf("late = %v", err)
	}
	if err := ledger.Register(RequestInput, " request-3 "); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Resolve(RequestInput, "request-3"); err != nil {
		t.Fatalf("normalized request id = %v", err)
	}
}

func FuzzRequestLedgerNeverAcceptsDuplicate(f *testing.F) {
	f.Add("request-1")
	f.Add("")
	f.Fuzz(func(t *testing.T, requestID string) {
		ledger := NewRequestLedger()
		if err := ledger.Register(RequestApproval, requestID); err != nil {
			return
		}
		if err := ledger.Resolve(RequestApproval, requestID); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Resolve(RequestApproval, requestID); !protocol.IsCode(
			err,
			protocol.CodeConflict,
		) {
			t.Fatalf("duplicate resolution error = %v", err)
		}
	})
}
