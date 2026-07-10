package breaker

import (
	"sync"
	"time"
)

type state int

const (
	closed state = iota
	open
	halfOpen
)

type Breaker struct {
	mu                sync.Mutex
	state             state
	failures          int
	failuresThreshold int
	cooldown          time.Duration
	openedAt          time.Time
}

func (br *Breaker) Allow() bool {
	br.mu.Lock()
	defer br.mu.Unlock()

	switch br.state {
	case closed:
		return true

	case open:
		if time.Since(br.openedAt) >= br.cooldown {
			br.state = halfOpen

			return true
		}

		return false

	case halfOpen:
		return false

	default:
		return false
	}
}

// RecordSuccess reports whether this call changed the breaker's state (i.e.
// it wasn't already closed), so Registry can log transitions only instead of
// once per successful request.
func (br *Breaker) RecordSuccess() (changed bool) {
	br.mu.Lock()
	defer br.mu.Unlock()

	wasOpen := br.state != closed

	br.failures = 0
	br.state = closed

	return wasOpen
}

// RecordFailure reports whether this call opened (or reopened) the breaker.
func (br *Breaker) RecordFailure() (changed bool) {
	br.mu.Lock()
	defer br.mu.Unlock()

	switch br.state {
	case closed:
		br.failures += 1

		if br.failures >= br.failuresThreshold {
			br.state = open
			br.openedAt = time.Now()
			return true
		}
	case halfOpen:
		br.state = open
		br.openedAt = time.Now()
		return true
	}

	return false
}
