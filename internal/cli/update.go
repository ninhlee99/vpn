package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"vpn/internal/privilege"
)

// releaseAssetBaseURL is where CI publishes prebuilt binaries — see
// .github/workflows/release.yml, which names each one vpn-darwin-<GOARCH>.
const releaseAssetBaseURL = "https://github.com/ninhlee99/vpn/releases/latest/download/"

func cmdUpdate(args []string) error {
	buildOut, version, err := downloadRelease()
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

// downloadRelease fetches this machine's architecture's prebuilt binary from
// the latest GitHub release. Every install/update path uses this binary-only
// flow; it never clones or builds source on the target machine. The same
// binary works for anyone because the per-install owner restriction lives in
// privilege.OwnerFile, not in a build-time constant.
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

	// CreateTemp avoids a predictable name in a shared directory. The file is
	// downloaded before privilege is raised, then later copied by the setuid
	// process into /usr/local/bin.
	f, err := os.CreateTemp("", "vpn-update-download-*")
	if err != nil {
		return "", "", err
	}
	out := f.Name()
	if err := f.Chmod(0o755); err != nil {
		f.Close()
		os.Remove(out)
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
