package testutil

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func ConfigLogging() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
}
