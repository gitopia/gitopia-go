package logger

import (
	"context"
	"log"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const LOG_FILE_EXT = ".log"
const LOG_FILE_PERM = 0777

type logctx struct{}

func InitLogger(ctx context.Context, appName string) context.Context {
	logger := logrus.New()

	// current directory if WORKING_DIR is not configured	
	file, err := os.OpenFile(viper.GetString("WORKING_DIR")+appName+LOG_FILE_EXT, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LOG_FILE_PERM)
	if err != nil {
		log.Fatalln("error opening log file: ", err.Error())
	}
	logger.SetOutput(file)
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
