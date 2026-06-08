package tui

import (
	"io"
	"strconv"
	"strings"
	"time"
)

type terminalProfile struct {
	supportsANSI bool
	baudDelay    time.Duration
	locale       localeCode
}

func terminalProfileFromEnviron(env []string) terminalProfile {
	values := parseEnviron(env)
	return terminalProfile{
		supportsANSI: supportsANSI(values),
		baudDelay:    baudDelayFromSetting(values["BUDGIE_BAUD"]),
		locale:       localeFromEnviron(env),
	}
}

func parseEnviron(env []string) map[string]string {
	values := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}

func supportsANSI(values map[string]string) bool {
	term := strings.ToLower(values["TERM"])
	colorTerm := strings.ToLower(values["COLORTERM"])

	if term == "" && colorTerm == "" {
		return false
	}
	if term == "" && colorTerm != "" {
		return true
	}
	if strings.Contains(term, "dumb") {
		return false
	}
	if strings.Contains(term, "vt100") || strings.Contains(term, "vt102") || strings.Contains(term, "vt220") {
		return false
	}

	return true
}

func baudDelayFromSetting(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	bits := 0
	if v != "" {
		var err error
		bits, err = strconv.Atoi(v)
		if err != nil || bits <= 0 {
			return 0
		}
	}
	if bits < 300 {
		return 0
	}
	bitsPerChar := bits / 10
	if bitsPerChar <= 0 {
		return 0
	}
	return time.Second / time.Duration(bitsPerChar)
}

type baudWriter struct {
	w     io.Writer
	delay time.Duration
}

func newBaudWriter(w io.Writer, delay time.Duration) io.Writer {
	if w == nil || delay <= 0 {
		return w
	}
	return &baudWriter{
		w:     w,
		delay: delay,
	}
}

func (w *baudWriter) Write(p []byte) (int, error) {
	if w.delay <= 0 {
		return w.w.Write(p)
	}
	total := 0
	for i := 0; i < len(p); i++ {
		n, err := w.w.Write(p[i : i+1])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
		if i+1 < len(p) {
			time.Sleep(w.delay)
		}
	}
	return total, nil
}
