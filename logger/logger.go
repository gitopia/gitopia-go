package logger

import (
	"context"

	"github.com/sirupsen/logrus"
)

const LOG_FILE_EXT = ".log"
const LOG_FILE_PERM = 0777

type logctx struct{}

func InitLogger(ctx context.Context, appName string) context.Context {
	logger := logrus.New()

	// todo: make configurable
	logger.SetLevel(logrus.DebugLevel)
	return ContextWithValue(ctx, logger)
}

func ContextWithValue(ctx context.Context, l *logrus.Logger) context.Context {
	return context.WithValue(ctx, logctx{}, l)
}

func FromContext(ctx context.Context) *logrus.Logger {
	return ctx.Value(logctx{}).(*logrus.Logger)
}
