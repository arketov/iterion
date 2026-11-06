package server

import (
	"github.com/arketov/iterion/internal/httpx"
	"github.com/arketov/iterion/internal/pathredact"
)

// installPathRedaction makes the client errors of this process stop naming the
// host's directories.
//
// A handler that formats the error of a file operation into a 4xx
// (`load diagnostic: open /srv/store/runs/x/run.json: no such file`) used to
// hand the caller the layout of the machine — for the studio a local detail,
// for a cloud server the tenant's view of the platform's storage. A 5xx body is
// already generic (internal/httpx.WriteErrorJSON logs the cause); this closes
// the 4xx side at the one chokepoint every httpError/httpErrorFor/httpErrorCode
// call reaches, instead of at the hundreds of call sites.
//
// workDir and storeDir are the roots the redaction cuts a path at, so a message
// keeps what is relative to the project (`.iterion/runs/x/run.json`) and a
// space in a directory name does not leave the tail of the root behind. Both
// are additive: a studio that switches project registers the new roots and the
// old ones stay (the registry is bounded), because an error may still quote a
// path of the project just left. The redactor itself is process-wide and
// idempotent, like the logger it sits beside.
func installPathRedaction(workDir, storeDir string) {
	pathredact.AddRoots(workDir, storeDir)
	httpx.SetErrorRedactor(pathredact.Host)
}
