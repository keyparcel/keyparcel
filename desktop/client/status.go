package main

import "time"

type confirmation struct {
	Text  string
	Until time.Time
}

func (c confirmation) current(now time.Time) string {
	if !now.Before(c.Until) {
		return ""
	}
	return c.Text
}
