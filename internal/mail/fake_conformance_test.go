package mail_test

import (
	"testing"

	"github.com/julianknutsen/gascity/internal/mail"
	"github.com/julianknutsen/gascity/internal/mail/mailtest"
)

func TestFakeConformance(t *testing.T) {
	mailtest.RunProviderTests(t, func(_ *testing.T) mail.Provider {
		return mail.NewFake()
	})
}
