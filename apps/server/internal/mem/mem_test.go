package mem

import (
	"regexp"
	"testing"
)

func TestLine(t *testing.T) {
	if !regexp.MustCompile(`^rss=\d+ heapInuse=\d+ heapSys=\d+ MB$`).MatchString(Line()) || RSS() == 0 {
		t.Fatalf("%q", Line())
	}
}
