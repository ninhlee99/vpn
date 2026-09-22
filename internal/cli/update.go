package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"vpn/internal/privilege"
)

// SourceDir is where `vpn update` finds the source tree to rebuild from —
// set at build time by install.sh via -ldflags "-X main.sourceDir=...", to
// wherever the repo lived when it ran. Empty for a binary built any other
// way (plain `go build`), in which case update just tells the user so.
var SourceDir string

// AllowedUID is the uid baked in by install.sh that CheckOwner enforces
// (see main.go / internal/privilege) — update must re-embed the *same*
// value into the rebuilt binary, or the rebuild would silently come out
// with no owner restriction at all and any local user could run it.
var AllowedUID string

func cmdUpdate(args []string) error {
	if SourceDir == "" {
		return fmt.Errorf("this binary doesn't know its source directory (not installed via install.sh) — cd into the repo and run ./install.sh again")
	}
	if _, err := os.Stat(SourceDir); err != nil {
		return fmt.Errorf("source directory %s is gone — cd into the repo and run ./install.sh again: %w", SourceDir, err)
	}

	// git pull + go build both run unprivileged (as whoever invoked
	// `update`), same as a normal build — no reason for either to touch
	// root. Only the final install step (copying the new binary over
	// /usr/local/bin/vpn and re-setting the setuid bit) needs it.
	if _, err := os.Stat(filepath.Join(SourceDir, ".git")); err == nil {
		fmt.Println("Đang git pull...")
		out, err := runIn(SourceDir, "git", "pull", "--ff-only")
		fmt.Print(out)
		if err != nil {
			return fmt.Errorf("git pull thất bại: %w", err)
		}
	}

	version := gitVersion(SourceDir)
	fmt.Printf("Đang build %s...\n", version)
	buildOut := filepath.Join(os.TempDir(), "vpn-update-build")
	ldflags := fmt.Sprintf("-X main.version=%s -X main.sourceDir=%s -X main.allowedUID=%s", version, SourceDir, AllowedUID)
	if out, err := runIn(SourceDir, "go", "build", "-ldflags", ldflags, "-o", buildOut, "./cmd/vpn"); err != nil {
		fmt.Print(out)
		return fmt.Errorf("build thất bại: %w", err)
	}
	defer os.Remove(buildOut)

	installPath := "/usr/local/bin/vpn"
	if err := privilege.Elevate(func() error {
		return installBinary(buildOut, installPath)
	}); err != nil {
		return fmt.Errorf("cài bản mới vào %s thất bại: %w", installPath, err)
	}

	fmt.Printf("Đã cập nhật lên %s.\n", version)
	return nil
}

// installBinary copies src over dst and re-applies the setuid-root bit,
// exactly like install.sh's own install step — must run while
// privilege.Elevate has raised this process to root, since both the
// destination directory and the chown target require it.
func installBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chown(tmp, 0, 0); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("chown root:wheel: %w", err)
	}
	// syscall.Chmod, not os.Chmod: Go's os.FileMode doesn't map a plain
	// numeric 0o4755 to the setuid bit the way the raw chmod(2) syscall
	// (and the `chmod` shell command) does — os.ModeSetuid is a distinct,
	// much higher bit in FileMode's own encoding, so os.Chmod(tmp, 0o4755)
	// would silently NOT set setuid. The raw syscall takes the standard
	// Unix mode bits directly, same as `chmod 4755`.
	if err := syscall.Chmod(tmp, 0o4755); err != nil { // 04755: setuid + rwxr-xr-x
		os.Remove(tmp)
		return fmt.Errorf("chmod u+s: %w", err)
	}
	return os.Rename(tmp, dst)
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// gitVersion returns `git describe` for dir if it's a git repo with any
// history, else falls back to the version already baked into this binary
// (i.e. update rebuilds the same version string if it can't do better).
func gitVersion(dir string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return Version
	}
	v := string(bytes.TrimSpace(out))
	if v == "" {
		return Version
	}
	return v
}
