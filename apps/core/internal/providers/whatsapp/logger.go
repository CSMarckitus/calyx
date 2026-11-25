package whatsapp

import (
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogLogger sends whatsmeow's logs to slog. whatsmeow is chatty: only warnings and errors keep
// their level, the rest is debug.
type slogLogger struct {
	log *slog.Logger
}

func newLogger(instanceName string) waLog.Logger {
	return slogLogger{log: slog.With("instance", instanceName, "module", "whatsmeow")}
}

func (l slogLogger) Errorf(msg string, args ...any) { l.log.Error(fmt.Sprintf(msg, args...)) }
func (l slogLogger) Warnf(msg string, args ...any)  { l.log.Warn(fmt.Sprintf(msg, args...)) }
func (l slogLogger) Infof(msg string, args ...any)  { l.log.Debug(fmt.Sprintf(msg, args...)) }
func (l slogLogger) Debugf(msg string, args ...any) { l.log.Debug(fmt.Sprintf(msg, args...)) }
func (l slogLogger) Sub(module string) waLog.Logger {
	return slogLogger{log: l.log.With("module", "whatsmeow/"+module)}
}
