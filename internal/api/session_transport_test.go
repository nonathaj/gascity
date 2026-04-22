package api

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

type createTransportCapableProvider struct {
	*runtime.Fake
}

func (p *createTransportCapableProvider) SupportsTransport(transport string) bool {
	return transport == "acp"
}

func TestProviderSessionTransportUsesExplicitACPConfigOnCustomProvider(t *testing.T) {
	transport, err := providerSessionTransport(&config.ResolvedProvider{
		Name:        "custom-acp",
		SupportsACP: true,
		ACPCommand:  "/bin/echo",
	}, &createTransportCapableProvider{Fake: runtime.NewFake()})
	if err != nil {
		t.Fatalf("providerSessionTransport: %v", err)
	}
	if transport != "acp" {
		t.Fatalf("providerSessionTransport() = %q, want %q", transport, "acp")
	}
}

func TestProviderSessionTransportSupportsACPAloneStaysDefault(t *testing.T) {
	transport, err := providerSessionTransport(&config.ResolvedProvider{
		Name:        "custom-acp",
		SupportsACP: true,
	}, &createTransportCapableProvider{Fake: runtime.NewFake()})
	if err != nil {
		t.Fatalf("providerSessionTransport: %v", err)
	}
	if transport != "" {
		t.Fatalf("providerSessionTransport() = %q, want empty transport", transport)
	}
}
