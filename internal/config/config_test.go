package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingFileIsDefaults(t *testing.T) {
	cfg, unknown, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(unknown) != 0 {
		t.Fatalf("err=%v unknown=%v", err, unknown)
	}
	if cfg.Theme != "amber" || len(cfg.DenseModules) != 4 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestUnknownKeysReported(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("theme = \"mono\"\nbogus = 1\nacks = [\"samba\"]\n"), 0o644) //nolint:errcheck
	cfg, unknown, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "mono" || !cfg.Acked("samba") || cfg.Acked("nfs") {
		t.Errorf("parse wrong: %+v", cfg)
	}
	if len(unknown) != 1 || unknown[0] != "bogus" {
		t.Errorf("unknown = %v", unknown)
	}
}

func TestMalformedIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("theme = [oops\n"), 0o644) //nolint:errcheck
	if _, _, err := LoadFrom(p); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	want := Default()
	want.Acks = []string{"nfs"}
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadFrom(p)
	if err != nil || !got.Acked("nfs") || got.Theme != "amber" {
		t.Errorf("round trip: %+v err=%v", got, err)
	}
}
