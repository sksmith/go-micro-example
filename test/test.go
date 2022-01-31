package test

import "runtime"

type CallWatcher struct {
	functionCalls map[string][][]interface{}
}

func NewCallWatcher() *CallWatcher {
	return &CallWatcher{functionCalls: make(map[string][][]interface{})}
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
	funcName := frame.Func.Name()

	calls := w.functionCalls[funcName]
	w.functionCalls[funcName] = append(calls, args)
}
