package testutil

// SelfExpiringHoldScript is a portable /bin/sh script body for a fake
// long-lived subprocess (poller stand-ins). It stays alive long enough
// for any test that watches it, then exits on its own, so an orphaned
// copy cannot outlive a killed test run (Windows never tears down
// process trees — incident gw-qhs). The bounded sleep loop is used
// instead of `read -t`, a bashism dash lacks, and instead of blocking
// on stdin forever.
const SelfExpiringHoldScript = "#!/bin/sh\nn=0\nwhile [ \"$n\" -lt 300 ]; do sleep 1; n=$((n+1)); done\n"
