package herdr

import (
	"sort"
	"time"
)

// This file is the package's test seam for callers outside it. The in-package
// fakes build a Provider by setting unexported fields, which a test in another
// package (cmd/gc's nudge poller tests) cannot do. Nothing in production calls
// these.

// NewWithEndpoints builds a Provider like [New], but talking to the given
// herdr executable and unix socket instead of the installed `herdr` and the
// session's own socket. It exists so a test in another package can drive the
// real Provider — its CLI calls, its socket activity tracker — over a stub
// herdr. An empty bin or sockPath keeps [New]'s value for that endpoint.
func NewWithEndpoints(herdrSession, metaDir, cityRoot, bin, sockPath string, setupTimeout, setupMaxTimeout time.Duration) *Provider {
	p := New(herdrSession, metaDir, cityRoot, setupTimeout, setupMaxTimeout)
	if bin != "" {
		p.c.bin = bin
	}
	if sockPath != "" {
		p.c.sockPath = sockPath
	}
	return p
}

// ActivityKeys returns the names the activity tracker currently files its
// entries under, sorted. [Provider.GetLastActivity] answers by exact match on
// these, so a test (or a diagnostic) can see whether a session name the caller
// asks about is one the tracker knows. It does not start the tracker: before
// the first GetLastActivity call it returns nil.
func (p *Provider) ActivityKeys() []string {
	p.act.mu.Lock()
	defer p.act.mu.Unlock()
	if len(p.act.entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.act.entries))
	for k := range p.act.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
