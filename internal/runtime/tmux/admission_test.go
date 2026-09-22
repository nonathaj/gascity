package tmux

import (
	"context"
	"errors"
	"testing"
)

func TestAdmissionCensusDistinguishesAbsentAndUnreachableServer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		observation error
		wantErr     bool
	}{
		{"confirmed-absent", nil, false},
		{"live-but-unreachable", errors.New("socket still has a live peer"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProviderWithConfig(Config{SocketName: "admission-test"})
			p.tm.exec = &fakeExecutor{err: ErrNoServer}
			p.tm.serverSocketObserver = func(context.Context, string) error { return tc.observation }
			names, err := p.ListSessionsForAdmission(context.Background())
			if (err != nil) != tc.wantErr || len(names) != 0 {
				t.Fatalf("names=%v err=%v, want error=%v", names, err, tc.wantErr)
			}
		})
	}
}
