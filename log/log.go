package log

import "github.com/rs/zerolog"

var defaultLog = GetLogger("default")

func Error() *zerolog.Event {
	return defaultLog.Error()
}

func Info() *zerolog.Event {
	return defaultLog.Info()
}

func Debug() *zerolog.Event {
	return defaultLog.Debug()
}

func Fatal() *zerolog.Event {
	return defaultLog.Fatal()
}

func Trace() *zerolog.Event {
	return defaultLog.Trace()
}
