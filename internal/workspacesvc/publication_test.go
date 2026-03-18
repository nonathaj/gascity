package workspacesvc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/supervisor"
)

func TestDerivePublishedURL(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "Acme",
		PublicBaseDomain: "apps.example.com",
	}, publicationRefs{}, "Demo City", config.Service{
		Name: "review_intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if reason != "route_active" {
		t.Fatalf("reason = %q, want route_active", reason)
	}
	if !strings.HasPrefix(url, "https://review-intake--demo-city--acme--") {
		t.Fatalf("url = %q, want review-intake--demo-city--acme prefix", url)
	}
	if !strings.HasSuffix(url, ".apps.example.com") {
		t.Fatalf("url = %q, want apps.example.com suffix", url)
	}
}

func TestDerivePublishedURLUsesAuthoritativeMetadataWhenAvailable(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "Acme",
		PublicBaseDomain: "apps.example.com",
	}, publicationRefs{
		refs: map[string]supervisor.PublishedServiceRef{
			"review_intake": {
				ServiceName: "review_intake",
				Visibility:  "public",
				URL:         "https://review-intake--acme--deadbeef.apps.example.com",
			},
		},
		exists: true,
	}, "Demo City", config.Service{
		Name: "review_intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if reason != "route_active" {
		t.Fatalf("reason = %q, want route_active", reason)
	}
	if url != "https://review-intake--acme--deadbeef.apps.example.com" {
		t.Fatalf("url = %q, want authoritative hosted route", url)
	}
}

func TestDerivePublishedURLRequiresSupervisor(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{}, publicationRefs{}, "Demo", config.Service{
		Name: "review-intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_requires_supervisor" {
		t.Fatalf("reason = %q, want publication_requires_supervisor", reason)
	}
}

func TestDerivePublishedURLRequiresTenantSlug(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		PublicBaseDomain: "apps.example.com",
	}, publicationRefs{}, "Demo", config.Service{
		Name: "review-intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_tenant_slug_missing" {
		t.Fatalf("reason = %q, want publication_tenant_slug_missing", reason)
	}
}

func TestDerivePublishedURLRequiresTenantAuthForTenantVisibility(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "acme",
		TenantBaseDomain: "tenant.apps.example.com",
	}, publicationRefs{}, "Demo", config.Service{
		Name: "review-intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "tenant",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_tenant_auth_policy_missing" {
		t.Fatalf("reason = %q, want publication_tenant_auth_policy_missing", reason)
	}
}

func TestDerivePublishedURLRejectsOverlongHostname(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       strings.Repeat("tenant", 8),
		PublicBaseDomain: strings.Repeat("example", 20) + ".com",
	}, publicationRefs{}, strings.Repeat("workspace", 8), config.Service{
		Name: strings.Repeat("service", 8),
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_hostname_too_long" {
		t.Fatalf("reason = %q, want publication_hostname_too_long", reason)
	}
}

func TestDerivePublishedURLBlocksHostedFallbackWhenAuthoritativeStoreExists(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "Acme",
		PublicBaseDomain: "apps.example.com",
	}, publicationRefs{exists: true}, "Demo City", config.Service{
		Name: "review_intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_platform_url_missing" {
		t.Fatalf("reason = %q, want publication_platform_url_missing", reason)
	}
}

func TestDerivePublishedURLReportsPublicationMetadataInvalid(t *testing.T) {
	url, reason := derivePublishedURL(supervisor.PublicationConfig{
		Provider:         "hosted",
		TenantSlug:       "Acme",
		PublicBaseDomain: "apps.example.com",
	}, publicationRefs{
		exists: true,
		err:    fmt.Errorf("decode publication store: boom"),
	}, "Demo City", config.Service{
		Name: "review_intake",
		Publication: config.ServicePublicationConfig{
			Visibility: "public",
		},
	})
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
	if reason != "publication_metadata_invalid" {
		t.Fatalf("reason = %q, want publication_metadata_invalid", reason)
	}
}
