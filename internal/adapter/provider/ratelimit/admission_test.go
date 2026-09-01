package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestDecideUnknownWithoutContractOrHeader(t *testing.T) {
	var controller Controller
	decision := controller.Decide("route", 700_000, 0, time.Now())
	if decision.Status != StatusAdmit || decision.Source != SourceUnknown {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideRefusesRequestLargerThanKnownBurst(t *testing.T) {
	var controller Controller
	now := time.Now()
	decision := controller.Decide("route", 700_000, 500_000, now)
	if decision.Status != StatusRefuse ||
		decision.Reason != ReasonExceedsBurst ||
		decision.Source != SourceOperator {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestTryReserveThenWaitWhenRollingWindowIsFull(t *testing.T) {
	var controller Controller
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first := controller.TryReserve("route", 400_000, 500_000, now)
	if first.Status != StatusAdmit {
		t.Fatalf("first = %+v", first)
	}
	second := controller.TryReserve("route", 200_000, 500_000, now)
	if second.Status != StatusWait || second.Wait <= 0 {
		t.Fatalf("second = %+v", second)
	}
	later := controller.Decide("route", 200_000, 500_000, now.Add(TokenWindow))
	if later.Status != StatusAdmit {
		t.Fatalf("after window = %+v", later)
	}
}

func TestHeaderRemainingRefusesWithoutInventingLimit(t *testing.T) {
	var controller Controller
	controller.Observe(
		"route",
		0,
		http.StatusOK,
		http.Header{"X-Ratelimit-Remaining-Tokens": {"1000"}},
		nil,
	)
	decision := controller.Decide("route", 2000, 0, time.Now())
	if decision.Status != StatusRefuse && decision.Status != StatusWait {
		t.Fatalf("header remaining decision = %+v", decision)
	}
	if decision.Source != SourceHeader || decision.Available != 1000 {
		t.Fatalf("available = %+v", decision)
	}
}

func TestGenericRateLimitRemainingIsNotTokenContract(t *testing.T) {
	var controller Controller
	controller.Observe(
		"route",
		0,
		http.StatusOK,
		http.Header{
			"RateLimit-Limit":     {"100"},
			"RateLimit-Remaining": {"10"},
		},
		nil,
	)
	decision := controller.Decide("route", 700_000, 0, time.Now())
	if decision.Status != StatusAdmit || decision.Source != SourceUnknown {
		t.Fatalf("generic headers must not invent TPM: %+v", decision)
	}
}

func TestSuccessfulResponseDoesNotDropReservedTokens(t *testing.T) {
	var controller Controller
	now := time.Now()
	first := controller.TryReserve("route", 400_000, 500_000, now)
	if first.Status != StatusAdmit {
		t.Fatalf("first = %+v", first)
	}
	if err := controller.Observe("route", 0, http.StatusOK, http.Header{}, nil); err != nil {
		t.Fatal(err)
	}
	second := controller.Decide("route", 200_000, 500_000, now)
	if second.Status != StatusWait && second.Status != StatusRefuse {
		t.Fatalf("reserved tokens vanished after success: %+v", second)
	}
}

func TestHeaderLimitTokensBecomesBurstContract(t *testing.T) {
	var controller Controller
	controller.Observe(
		"route",
		0,
		http.StatusTooManyRequests,
		http.Header{
			"X-Ratelimit-Limit-Tokens":     {"500000"},
			"X-Ratelimit-Remaining-Tokens": {"0"},
			"Retry-After":                  {"1"},
		},
		nil,
	)
	decision := controller.Decide("route", 700_000, 0, time.Now())
	if decision.Status != StatusRefuse ||
		decision.Reason != ReasonExceedsBurst ||
		decision.Limit != 500_000 {
		t.Fatalf("header burst = %+v", decision)
	}
}
