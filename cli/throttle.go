package cli

import (
	"sync"
	"time"
)

type Throttle struct {
	rate     int // requests per second
	requests int // total requests
}

// simple rate limitter
func (l Throttle) RateLimit(f func()) {
	executed := 0

	ticker := time.Tick(time.Second)
	var wg = &sync.WaitGroup{}
	wg.Add(l.requests)
	for ; true; <-ticker {
		if executed >= l.requests {
			break
		}
		for i := 0; i < l.rate; i++ {
			go func() {
				f()
				executed++
				wg.Done()
			}()
		}
	}
	wg.Wait()
}
