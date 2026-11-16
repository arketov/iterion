package runtime

import "github.com/arketov/iterion/pkg/claw/internal/runtime/lane"

func init() {
	// Wire the summary compressor into the lane package to avoid a circular
	// import (lane cannot import runtime). Finished() events will now
	// compress their detail string using the default budget.
	lane.DetailCompressor = CompressSummaryText
}
