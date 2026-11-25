package retry

import (
	"context"
	"time"
)

const (
	Attempts   = 5
	FirstDelay = 100 * time.Millisecond
	Factor     = 2
	MaxDelay   = 2 * time.Second
)

type Policy struct {
	Attempts int
	First    time.Duration
	Factor   int
	Max      time.Duration
}

func Default() Policy {
	return Policy{Attempts: Attempts, First: FirstDelay, Factor: Factor, Max: MaxDelay}
}

type Sleeper func(ctx context.Context, delay time.Duration) error

func Do(ctx context.Context, op func() error) error {
	return DoWith(ctx, Default(), sleep, op)
}

func DoWith(ctx context.Context, policy Policy, sleep Sleeper, op func() error) error {
	delay := policy.First
	var err error
	for attempt := 1; attempt <= policy.Attempts; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = op()
		if err == nil {
			return nil
		}
		if attempt == policy.Attempts {
			break
		}
		if sleepErr := sleep(ctx, delay); sleepErr != nil {
			return sleepErr
		}
		delay *= time.Duration(policy.Factor)
		if delay > policy.Max {
			delay = policy.Max
		}
	}
	return err
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
