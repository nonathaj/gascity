package beadmail

import (
	"testing"

	"github.com/julianknutsen/gascity/internal/beads"
	"github.com/julianknutsen/gascity/internal/mail"
	"github.com/julianknutsen/gascity/internal/mail/mailtest"
)

func TestBeadmailConformance(t *testing.T) {
	mailtest.RunProviderTests(t, func(_ *testing.T) mail.Provider {
		return New(beads.NewMemStore())
	})
}
