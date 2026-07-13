package breaker

import (
	"testing"
	"time"
)

func newTestBreaker(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{failuresThreshold: threshold, cooldown: cooldown}
}

func TestBreaker_ClosedAllowsUntilThresholdReached(t *testing.T) {
	b := newTestBreaker(3, time.Hour)

	for i := 0; i < 2; i++ {
		if !b.Allow() {
			t.Fatalf("Allow() = false before threshold reached (after %d failures)", i)
		}
		b.RecordFailure()
	}

	if !b.Allow() {
		t.Fatal("Allow() = false, want true: only 2 of 3 threshold failures recorded")
	}
}

func TestBreaker_OpensAtThresholdAndRejects(t *testing.T) {
	b := newTestBreaker(2, time.Hour) // long cooldown: must not accidentally half-open mid-test

	b.RecordFailure()
	b.RecordFailure() // crosses the threshold

	if b.Allow() {
		t.Fatal("Allow() = true, want false: breaker should be open")
	}
}

func TestBreaker_HalfOpensAfterCooldownAndAllowsExactlyOneTrial(t *testing.T) {
	b := newTestBreaker(1, 10*time.Millisecond)
	b.RecordFailure() // opens immediately, threshold is 1

	if b.Allow() {
		t.Fatal("Allow() = true immediately after opening, want false: cooldown hasn't elapsed")
	}

	time.Sleep(20 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("Allow() = false after cooldown elapsed, want true: this call should become the half-open trial")
	}
	if b.Allow() {
		t.Fatal("Allow() = true for a second call during half-open, want false: only one trial at a time")
	}
}

func TestBreaker_SuccessfulTrialCloses(t *testing.T) {
	b := newTestBreaker(1, 10*time.Millisecond)
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	b.Allow() // consumes the trial slot, transitions to half-open

	if changed := b.RecordSuccess(); !changed {
		t.Error("RecordSuccess() changed = false, want true: breaker was half-open, not already closed")
	}
	if !b.Allow() {
		t.Fatal("Allow() = false after a successful trial, want true: breaker should be fully closed")
	}
}

func TestBreaker_FailedTrialReopensImmediately(t *testing.T) {
	b := newTestBreaker(1, 10*time.Millisecond)
	b.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	b.Allow() // consumes the trial slot

	if changed := b.RecordFailure(); !changed {
		t.Error("RecordFailure() changed = false, want true: a failed half-open trial must reopen the breaker")
	}
	if b.Allow() {
		t.Fatal("Allow() = true immediately after a failed trial, want false: cooldown should have restarted")
	}
}

func TestBreaker_RecordSuccessResetsFailureStreak(t *testing.T) {
	b := newTestBreaker(3, time.Hour)
	b.RecordFailure()
	b.RecordFailure() // 2 of 3 — one more would open it
	b.RecordSuccess() // should reset the streak back to 0

	b.RecordFailure()
	b.RecordFailure() // back to 2 of 3 again, not 4 of 3

	if !b.Allow() {
		t.Fatal("Allow() = false, want true: failure count should have reset after RecordSuccess")
	}
}

func TestBreaker_RecordSuccessOnAlreadyClosedReportsNoChange(t *testing.T) {
	b := newTestBreaker(3, time.Hour)

	if changed := b.RecordSuccess(); changed {
		t.Error("RecordSuccess() changed = true on an already-closed breaker, want false")
	}
}
