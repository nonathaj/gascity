package auto

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

type admissionCensusProvider struct {
	*runtime.Fake
	err error
}

func (p admissionCensusProvider) ListSessionsForAdmission(context.Context) ([]string, error) {
	return []string{"pending"}, p.err
}

func TestAdmissionCensusPreservedAcrossRouter(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var err error
		if fail {
			err = errors.New("incomplete")
		}
		p := New(admissionCensusProvider{runtime.NewFake(), err}, runtime.NewFake())
		names, gotErr := p.ListSessionsForAdmission(context.Background())
		if (gotErr != nil) != fail || len(names) != 1 || names[0] != "pending" {
			t.Fatalf("names=%v err=%v, want pending and error=%v", names, gotErr, fail)
		}
	}
}
