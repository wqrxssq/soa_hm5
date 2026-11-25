package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultBackoff(t *testing.T) {
	var slept []time.Duration
	attempts := 0
	err := DoWith(context.Background(), Default(), func(_ context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}, func() error {
		attempts++
		return errors.New("fail")
	})
	if err == nil || attempts != Attempts {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
	}
	if len(slept) != len(want) {
		t.Fatalf("sleeps=%v", slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("sleeps=%v", slept)
		}
	}
}

func TestDelayCap(t *testing.T) {
	var slept []time.Duration
	policy := Policy{Attempts: 3, First: 1500 * time.Millisecond, Factor: 2, Max: MaxDelay}
	err := DoWith(context.Background(), policy, func(_ context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	}, func() error {
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(slept) != 2 || slept[0] != 1500*time.Millisecond || slept[1] != MaxDelay {
		t.Fatalf("sleeps=%v", slept)
	}
}

func TestStopsOnSuccess(t *testing.T) {
	attempts := 0
	sleeps := 0
	err := DoWith(context.Background(), Default(), func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("fail")
		}
		return nil
	})
	if err != nil || attempts != 3 || sleeps != 2 {
		t.Fatalf("attempts=%d sleeps=%d err=%v", attempts, sleeps, err)
	}
}
