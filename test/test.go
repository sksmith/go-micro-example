package test

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

type CallWatcher struct {
	functionCalls map[string][][]interface{}
}

func NewCallWatcher() *CallWatcher {
	return &CallWatcher{functionCalls: make(map[string][][]interface{})}
}

func (w *CallWatcher) VerifyCount(funcName string, want int, t *testing.T) {
	if w.GetCallCount(funcName) != want {
		t.Errorf("%s call count got=%d want=%d", funcName, w.GetCallCount(funcName), want)
	}
}

func (w *CallWatcher) GetCall(funcName string) [][]interface{} {
	return w.functionCalls[funcName]
}

func (w *CallWatcher) GetCallCount(funcName string) int {
	return len(w.functionCalls[funcName])
}

func (w *CallWatcher) AddCall(args ...interface{}) {
	pc := make([]uintptr, 15)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])
	frame, _ := frames.Next()
	funcParts := strings.Split(frame.Function, ".")
	funcName := funcParts[len(funcParts)-1]

	calls := w.functionCalls[funcName]
	w.functionCalls[funcName] = append(calls, args)
}

func ConfigLogging() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
}
