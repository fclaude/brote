package std

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fclaude/brote/scheme"
)

const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

// atom returns the text of a string or symbol datum.
func atom(v scheme.Value) (string, bool) {
	if v.Kind == scheme.KString || v.Kind == scheme.KSymbol {
		return v.S, true
	}
	return "", false
}

// parseSize reads a byte count: an integer, or text such as 512KB, 16MB or 1GB
// (powers of 1024).
func parseSize(v scheme.Value) (int64, error) {
	if v.Kind == scheme.KInt {
		if v.I <= 0 {
			return 0, fmt.Errorf("size must be positive, got %d", v.I)
		}
		return v.I, nil
	}
	s, ok := atom(v)
	if !ok {
		return 0, fmt.Errorf("size must be a number of bytes or text like 16MB, got %s", scheme.Write(v))
	}
	u := strings.ToUpper(s)
	mult := int64(1)
	for _, suf := range []struct {
		s string
		m int64
	}{{"KB", kib}, {"MB", mib}, {"GB", gib}, {"B", 1}} {
		if strings.HasSuffix(u, suf.s) {
			u, mult = strings.TrimSuffix(u, suf.s), suf.m
			break
		}
	}
	n, err := strconv.ParseInt(u, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("size %q is not a positive size such as 512KB, 16MB or 1GB", s)
	}
	return n * mult, nil
}

// parseDuration reads a time span: an integer of seconds, or text such as
// 500ms, 10s or 2m.
func parseDuration(v scheme.Value) (time.Duration, error) {
	if v.Kind == scheme.KInt {
		if v.I <= 0 || v.I > 1<<31 {
			return 0, fmt.Errorf("duration must be a positive number of seconds, got %d", v.I)
		}
		return time.Duration(v.I) * time.Second, nil
	}
	s, ok := atom(v)
	if !ok {
		return 0, fmt.Errorf("duration must be seconds or text like 10s, got %s", scheme.Write(v))
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("duration %q is not a positive duration such as 500ms, 10s or 2m", s)
	}
	return d, nil
}
