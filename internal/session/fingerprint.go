package session

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"sort"
)

// ConfigFingerprint returns a deterministic hash of the Config fields that
// define an agent's behavioral identity. Changes to these fields indicate
// the agent should be restarted (via drain when drain ops are available).
//
// Included: Command, Env, FingerprintExtra (pool config, etc.),
// Nudge, PreStart, SessionSetup, SessionSetupScript, OverlayDir, CopyFiles.
//
// Excluded (observation-only hints): WorkDir, ReadyPromptPrefix,
// ReadyDelayMs, ProcessNames, EmitsPermissionWarning.
//
// The hash is a hex-encoded SHA-256. Same config always produces the same
// hash regardless of map iteration order.
func ConfigFingerprint(cfg Config) string {
	h := sha256.New()
	h.Write([]byte(cfg.Command)) //nolint:errcheck // hash.Write never errors
	h.Write([]byte{0})           //nolint:errcheck // hash.Write never errors

	hashSortedMap(h, cfg.Env)

	// FingerprintExtra carries additional identity fields (pool config, etc.)
	// that aren't part of the session command but should
	// trigger a restart on change. Prefixed with "fp:" to avoid collisions
	// with Env keys.
	if len(cfg.FingerprintExtra) > 0 {
		h.Write([]byte("fp")) //nolint:errcheck // hash.Write never errors
		h.Write([]byte{0})    //nolint:errcheck // hash.Write never errors
		hashSortedMap(h, cfg.FingerprintExtra)
	}

	// Nudge
	h.Write([]byte(cfg.Nudge)) //nolint:errcheck // hash.Write never errors
	h.Write([]byte{0})         //nolint:errcheck // hash.Write never errors

	// PreStart
	for _, ps := range cfg.PreStart {
		h.Write([]byte(ps)) //nolint:errcheck // hash.Write never errors
		h.Write([]byte{0})  //nolint:errcheck // hash.Write never errors
	}
	h.Write([]byte{1}) //nolint:errcheck // sentinel between slices

	// SessionSetup
	for _, ss := range cfg.SessionSetup {
		h.Write([]byte(ss)) //nolint:errcheck // hash.Write never errors
		h.Write([]byte{0})  //nolint:errcheck // hash.Write never errors
	}
	h.Write([]byte{1}) //nolint:errcheck // sentinel between slices

	h.Write([]byte(cfg.SessionSetupScript)) //nolint:errcheck // hash.Write never errors
	h.Write([]byte{0})                      //nolint:errcheck // hash.Write never errors

	h.Write([]byte(cfg.OverlayDir)) //nolint:errcheck // hash.Write never errors
	h.Write([]byte{0})              //nolint:errcheck // hash.Write never errors

	// CopyFiles
	for _, cf := range cfg.CopyFiles {
		h.Write([]byte(cf.Src))    //nolint:errcheck // hash.Write never errors
		h.Write([]byte(cf.RelDst)) //nolint:errcheck // hash.Write never errors
		h.Write([]byte{0})         //nolint:errcheck // hash.Write never errors
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// hashSortedMap writes map entries to h in deterministic sorted-key order.
func hashSortedMap(h hash.Hash, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))    //nolint:errcheck // hash.Write never errors
		h.Write([]byte{'='})  //nolint:errcheck // hash.Write never errors
		h.Write([]byte(m[k])) //nolint:errcheck // hash.Write never errors
		h.Write([]byte{0})    //nolint:errcheck // hash.Write never errors
	}
}
