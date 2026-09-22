package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"vpn/internal/privilege"
)

// SourceDir is where `vpn update` rebuilds from source if no prebuilt
// release binary is available for this machine's architecture — set at
// build time by install.sh via -ldflags "-X main.sourceDir=...", to
// wherever the repo lived when it ran. Empty for a binary built any other
// way (plain `go build`), in which case that fallback just isn't offered.
var SourceDir string

// releaseAssetBaseURL is where CI publishes prebuilt binaries — see
// .github/workflows/release.yml, which names each one vpn-darwin-<GOARCH>.
const releaseAssetBaseURL = "https://github.com/ninhlee99/vpn/releases/latest/download/"

func cmdUpdate(args []string) error {
	buildOut, version, err := fetchOrBuild()
	if err != nil {
		return err
	}
	defer os.Remove(buildOut)

	installPath := "/usr/local/bin/vpn"
	if err := privilege.Elevate(func() error {
		return installBinary(buildOut, installPath)
	}); err != nil {
		return fmt.Errorf("installing new build to %s failed: %w", installPath, err)
	}

	fmt.Printf("Updated to %s.\n", version)
	return nil
}

// fetchOrBuild tries a prebuilt release binary first — no Go toolchain
// needed on this machine at all — and only falls back to rebuilding from
// source (which does need one, see internal/cli/update.go's SourceDir doc
// comment) if that's unavailable, e.g. this architecture has no release
// asset yet, or this machine has no network access to github.com.
func fetchOrBuild() (path, version string, err error) {
	if path, version, err := downloadRelease(); err == nil {
		return path, version, nil
	} else {
		fmt.Printf("No prebuilt release available (%v) — building from source instead.\n", err)
	}
	return buildFromSource()
}

// downloadRelease fetches this machine's architecture's prebuilt binary
// from the latest GitHub release. The same binary works for anyone — the
// per-installing-user owner restriction lives in privilege.OwnerFile, a
// local file install.sh writes, not anything baked into the binary — so
// there's nothing machine- or user-specific CI needs to know to build it.
func downloadRelease() (path, version string, err error) {
	arch := runtime.GOARCH
	if arch != "arm64" && arch != "amd64" {
		return "", "", fmt.Errorf("no prebuilt release for GOARCH=%s", arch)
	}
	url := releaseAssetBaseURL + "vpn-darwin-" + arch

	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	out := filepath.Join(os.TempDir(), "vpn-update-download")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(out)
		return "", "", fmt.Errorf("save downloaded binary: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(out)
		return "", "", err
	}

	verOut, err := exec.Command(out, "version").Output()
	if err != nil {
		os.Remove(out)
		return "", "", fmt.Errorf("downloaded binary doesn't run: %w", err)
	}
	version = strings.TrimSpace(string(verOut))
	fmt.Printf("Downloaded prebuilt %s (%s)...\n", arch, version)
	return out, version, nil
}

// buildFromSource is the original vpn update path: git pull + go build,
// for machines with no prebuilt release available and a local checkout to
// build from.
func buildFromSource() (path, version string, err error) {
	if SourceDir == "" {
		return "", "", fmt.Errorf("this binary doesn't know its source directory (not installed via install.sh) — cd into the repo and run ./install.sh again")
	}
	if _, err := os.Stat(SourceDir); err != nil {
		return "", "", fmt.Errorf("source directory %s is gone — cd into the repo and run ./install.sh again: %w", SourceDir, err)
	}

	// git pull + go build both run unprivileged (as whoever invoked
	// `update`), same as a normal build — no reason for either to touch
	// root. Only the final install step (copying the new binary over
	// /usr/local/bin/vpn and re-setting the setuid bit) needs it.
	if _, err := os.Stat(filepath.Join(SourceDir, ".git")); err == nil {
		branch, err := runIn(SourceDir, "git", "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return "", "", fmt.Errorf("determine current git branch: %w", err)
		}
		branch = strings.TrimSpace(branch)
		fmt.Println("Pulling latest changes...")
		// Explicit "origin <branch>", not a bare `git pull` — that only
		// works if the local branch has upstream tracking configured,
		// which isn't guaranteed (e.g. a branch pushed with `git push
		// origin main` but never `--set-upstream`).
		out, err := runIn(SourceDir, "git", "pull", "--ff-only", "origin", branch)
		fmt.Print(out)
		if err != nil {
			return "", "", fmt.Errorf("git pull failed: %w", err)
		}
	}

	version = gitVersion(SourceDir)
	fmt.Printf("Building %s...\n", version)
	buildOut := filepath.Join(os.TempDir(), "vpn-update-build")
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.sourceDir=%s", version, SourceDir)
	if out, err := runIn(SourceDir, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", buildOut, "./cmd/vpn"); err != nil {
		fmt.Print(out)
		return "", "", fmt.Errorf("build failed: %w", err)
	}
	return buildOut, version, nil
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
