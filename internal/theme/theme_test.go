package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinThemesLoad(t *testing.T) {
	for _, name := range []string{"amber", "bbs", "mono", "nord", "terminal"} {
		s, err := Build(name, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.Name != name {
			t.Errorf("name = %q, want %q", s.Name, name)
		}
		if s.Glyph.OK != "●" {
			t.Errorf("%s: expected unicode glyphs by default", name)
		}
	}
}

func TestUnknownTheme(t *testing.T) {
	if _, err := Build("nope", ""); err == nil {
		t.Fatal("expected error for unknown theme")
	}
}

func TestUserDirOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "amber.toml"), []byte("ascii = true\n"), 0o644) //nolint:errcheck
	s, err := Build("amber", dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Glyph.OK != "*" {
		t.Errorf("user override not applied: glyph = %q", s.Glyph.OK)
	}
	if got := List(dir); len(got) != 5 {
		t.Errorf("List = %v, want amber+bbs+mono+nord+terminal", got)
	}
}
