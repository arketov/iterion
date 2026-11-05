package mail

import (
	"context"

	iterlog "github.com/arketov/iterion/pkg/log"
)

// LogMailer is the no-SMTP fallback: the email is written to the
// logger (token links included) so local-dev flows stay testable.
// Enabled() is false — the SPA hides email-dependent entry points.
type LogMailer struct{ Logger *iterlog.Logger }

func (l *LogMailer) Enabled() bool { return false }

func (l *LogMailer) Send(_ context.Context, m Message) error {
	if l.Logger != nil {
		l.Logger.Info("mail (log fallback): to=%s subject=%q\n%s", m.To, m.Subject, m.TextBody)
	}
	return nil
}
