package main

import "time"

func ptrString(s string) *string { return &s }

func nowNanos() int64 { return time.Now().UnixNano() }
