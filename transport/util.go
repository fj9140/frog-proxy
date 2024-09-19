package transport

import (
	"fmt"
	"strings"
)

type badStringError struct {
	what string
	str  string
}

func (e *badStringError) Error() string { return fmt.Sprintf("%s %q", e.what, e.str) }

func hasPort(addr string) bool {
	return strings.LastIndex(addr, ":") > strings.LastIndex(addr, "]")
}
