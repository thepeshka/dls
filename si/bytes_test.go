package si

import (
	"fmt"
	"testing"
)

func SprintfTest[T any](t *testing.T, format string, v T, target string) {
	if res := fmt.Sprintf(format, v); res != target {
		t.Errorf("expected %s, got %s", target, res)
	}
}

func TestFormatBase(t *testing.T) {
	v := NewBytes(1234567890)
	SprintfTest(t, "%.2f", v, "1.23 GB")
	SprintfTest(t, "%2.2f", v, "1.15 GiB")
	SprintfTest(t, "%#.2f", v, "9.88 Gb")
	SprintfTest(t, "%#2.2f", v, "9.20 Gib")
}
