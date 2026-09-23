package engine

import (
	"errors"
	"syscall"
	"testing"
)

func TestSendWithRouteRetry(t *testing.T) {
	attempts := 0
	repairs := 0
	err := sendWithRouteRetry(func() error {
		attempts++
		if attempts == 1 {
			return syscall.EHOSTUNREACH
		}
		return nil
	}, func() error {
		repairs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || repairs != 1 {
		t.Fatalf("attempts=%d repairs=%d, want 2 and 1", attempts, repairs)
	}
}

func TestSendWithRouteRetrySkipsOtherErrors(t *testing.T) {
	attempts := 0
	repairs := 0
	want := errors.New("write failed")
	err := sendWithRouteRetry(func() error {
		attempts++
		return want
	}, func() error {
		repairs++
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if attempts != 1 || repairs != 0 {
		t.Fatalf("attempts=%d repairs=%d, want 1 and 0", attempts, repairs)
	}
}
