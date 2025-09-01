package log

import (
	"arbitr/internal/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
)

type Logger = zerolog.Logger

func NewLogger(cfg config.Config) Logger {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	var l zerolog.Logger
	if cfg.Logging.Pretty {
		l = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		l = log.Logger
	}
	level, err := zerolog.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	return l
}
