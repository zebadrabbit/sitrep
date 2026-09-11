package collect

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"
)

func TestFixtureName(t *testing.T) {
	cases := map[string][]string{
		"ss_tulnpH.txt":              {"ss", "-tulnpH"},
		"who.txt":                    {"who"},
		"docker_ps_format_json_.txt": {"docker", "ps", "--format", "{{json .}}"},
	}
	for want, cmd := range cases {
		if got := FixtureName(cmd[0], cmd[1:]...); got != want {
			t.Errorf("%v → %q, want %q", cmd, got, want)
		}
	}
}

func TestDemoServesFixture(t *testing.T) {
	r := New("ports", true).WithFixtures(fstest.MapFS{
		"testdata/fixtures/ports/ss_tulnpH.txt": {Data: []byte("tcp LISTEN 0 1 *:22 *:*\n")},
	})
	res, err := r.Run(context.Background(), "ss", "-tulnpH")
	if err != nil || string(res.Stdout) == "" {
		t.Fatalf("err=%v out=%q", err, res.Stdout)
	}
	if _, err := r.Run(context.Background(), "ss", "-tunaH"); !errors.Is(err, ErrNoFixture) {
		t.Errorf("missing fixture: err=%v", err)
	}
}

func TestMissingBinary(t *testing.T) {
	_, err := New("x", false).Run(context.Background(), "definitely-not-a-binary-xyz")
	if !errors.Is(err, ErrMissing) {
		t.Errorf("err=%v", err)
	}
}

func TestTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New("x", false).Run(ctx, "sleep", "2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v", err)
	}
}
