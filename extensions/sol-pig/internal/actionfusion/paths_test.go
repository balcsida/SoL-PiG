package actionfusion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeToolPath(t *testing.T) {
	for _, sp := range []string{"\u00a0", "\u2000", "\u200a", "\u202f", "\u205f", "\u3000"} {
		if got := NormalizeToolPath("a" + sp + "b"); got != "a b" {
			t.Errorf("space %q: got %q", sp, got)
		}
	}
	for in, want := range map[string]string{"@README.md": "README.md", "@@x": "@x"} {
		if got := NormalizeToolPath(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNormalizeWindowsShellPath(t *testing.T) {
	for in, want := range map[string]string{
		`/c/src/app.ts`:     `C:\src\app.ts`,
		`/mnt/d/x`:          `D:\x`,
		`/cygdrive/e/y/z`:   `E:\y\z`,
		`/c`:                `C:\`,
		`/usr/local/bin/pi`: `/usr/local/bin/pi`,
		`//server/share`:    `//server/share`,
		`C:\native`:         `C:\native`,
	} {
		if got := NormalizeWindowsShellPath(in, "windows"); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	if got := NormalizeWindowsShellPath("/c/src/app.ts", "darwin"); got != "/c/src/app.ts" {
		t.Errorf("darwin: got %q", got)
	}
}

func TestResolveToolPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	for in, want := range map[string]string{
		"a/b.txt":               filepath.Join("/work", "a/b.txt"),
		"/abs/x":                "/abs/x",
		"file:///tmp/a%20b.txt": "/tmp/a b.txt",
		"file://localhost/x":    "/x",
		"@file:///tmp/c.txt":    "/tmp/c.txt",
		"~/x":                   filepath.Join(home, "x"),
		"~":                     home,
		"a\u00a0b.txt":          filepath.Join("/work", "a b.txt"),
	} {
		got, err := ResolveToolPath("/work", in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"file:///tmp/%zz", "file:///tmp/bad%2Fname.txt", "file://host/x"} {
		if got, err := ResolveToolPath("/work", bad); err == nil {
			t.Errorf("%q: expected error, got %q", bad, got)
		}
	}
}

func TestCanonicalQueueKey(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalQueueKey(filepath.Join(link, "new", "file.txt"))
	if want := filepath.Join(target, "new", "file.txt"); err != nil || got != want {
		t.Errorf("symlink: got %q, %v; want %q", got, err, want)
	}
	plain := filepath.Join(root, "missing.txt")
	if got, err := CanonicalQueueKey(plain); err != nil || got != plain {
		t.Errorf("plain: got %q, %v; want %q", got, err, plain)
	}
}
