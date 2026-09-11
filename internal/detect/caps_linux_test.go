//go:build linux

package detect

import "testing"

func TestCapsOf(t *testing.T) {
	status := []byte("Name:\tsitrep\nCapInh:\t0000000000000000\nCapEff:\t0000000000080004\nCapBnd:\t000001ffffffffff\n")
	if got := capsOf(status); got&wantMask != wantMask {
		t.Fatalf("CapEff 0x80004 should carry ptrace+dac_read_search, got %#x", got)
	}
	if capsOf([]byte("CapEff:\t0000000000000000\n"))&wantMask != 0 || capsOf(nil) != 0 {
		t.Fatal("empty set should not report caps")
	}
}
