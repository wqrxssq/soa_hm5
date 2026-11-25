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
