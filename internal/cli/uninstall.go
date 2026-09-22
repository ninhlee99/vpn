package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"vpn/internal/config"
	"vpn/internal/engine"
	"vpn/internal/keychain"
	"vpn/internal/privilege"
	"vpn/internal/state"
)

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask for confirmation")
	fs.Parse(args)

	if !*yes {
		fmt.Println("Sẽ xoá: binary /usr/local/bin/vpn, log, state, toàn bộ profile/account đã lưu (kể cả PSK/password trong Keychain).")
		fmt.Print("Chắc chắn xoá hết? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if line != "y\n" && line != "Y\n" {
			fmt.Println("Đã huỷ.")
			return nil
		}
	}

	// Tear down a live tunnel first — otherwise routes/DNS are left half
	// overridden with nothing left around to ever restore them.
	if st, err := state.Load(); err == nil && (st.Phase == state.PhaseConnected || st.Phase == state.PhaseConnecting) {
		fmt.Println("Đang ngắt kết nối...")
		if err := engine.Disconnect(); err != nil {
			fmt.Fprintf(os.Stderr, "cảnh báo: ngắt kết nối thất bại, tiếp tục xoá: %v\n", err)
		}
	}

	// Every profile/account's Keychain entry — config.Load never errors
	// on a missing file, so this is safe even on a barely-initialized
	// install.
	if cfg, err := config.Load(); err == nil {
		for profileName, p := range cfg.Profiles {
			_ = keychain.DeletePSK(profileName)
			for acctName := range p.Accounts {
				_ = keychain.DeletePassword(profileName, acctName)
			}
		}
	}

	// The user's own config dir (~/.config/vpn-l2tp) — unprivileged, it's
	// in their own home directory.
	if dir, err := config.Dir(); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "cảnh báo: không xoá được %s: %v\n", dir, err)
		}
	}

	// Everything else (log, state dir, the binary itself) is root-owned.
	err := privilege.Elevate(func() error {
		if err := os.RemoveAll(state.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "cảnh báo: không xoá được %s: %v\n", state.Dir, err)
		}
		if err := os.Remove(engine.LogPath()); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "cảnh báo: không xoá được %s: %v\n", engine.LogPath(), err)
		}
		// Delete the binary itself last — safe on Unix even though it's
		// the file this running process's own image was exec'd from
		// (removing a directory entry doesn't touch an already-open/
		// already-mapped inode, so this process keeps running fine).
		if err := os.Remove("/usr/local/bin/vpn"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("xoá /usr/local/bin/vpn: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Println("Đã gỡ vpn hoàn toàn.")
	return nil
}
