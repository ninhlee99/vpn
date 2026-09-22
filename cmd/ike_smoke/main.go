// Temporary smoke-test binary: runs Phase 1 against the real reference VPN
// server using credentials from ~/l2tp-proxy/.env, without ever printing
// the PSK. Not part of the shipped CLI — deleted once ike/l2tp/ppp are all
// wired into the real engine and this manual step is superseded by
// `l2tp-cli test`.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ninhlee99/vpn-l2tp/internal/config"
	"github.com/ninhlee99/vpn-l2tp/internal/ike"
)

func loadEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val := parts[1]
		if idx := strings.Index(val, "#"); idx >= 0 {
			val = val[:idx]
		}
		m[parts[0]] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return m, sc.Err()
}

func localOutboundIP() (net.IP, error) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return nil, err
	}
	var iface string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface:") {
			iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	out2, err := exec.Command("ifconfig", iface).Output()
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out2), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				return net.ParseIP(f[1]), nil
			}
		}
	}
	return nil, fmt.Errorf("could not determine local IP")
}

func main() {
	home, _ := os.UserHomeDir()
	env, err := loadEnv(home + "/l2tp-proxy/.env")
	if err != nil {
		fmt.Println("ERROR loading .env:", err)
		os.Exit(1)
	}
	server := env["VPN_SERVER"]
	psk := env["VPN_PSK"]
	if server == "" || psk == "" {
		fmt.Println("ERROR: VPN_SERVER or VPN_PSK missing from .env")
		os.Exit(1)
	}

	localIP, err := localOutboundIP()
	if err != nil {
		fmt.Println("ERROR determining local IP:", err)
		os.Exit(1)
	}
	fmt.Println("local IP:", localIP, "server:", server)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := ike.EstablishPhase1(ctx, ike.Config{
		ServerHost: server,
		PSK:        psk,
		Proposals:  config.DefaultIKEProposals,
		LocalIP:    localIP,
	})
	if err != nil {
		fmt.Println("PHASE1 FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("PHASE1 ESTABLISHED: nat_detected=%v transform={enc:%d hash:%d group:%d}\n",
		sess.NATDetected, sess.Transform.Encryption, sess.Transform.Hash, sess.Transform.Group)

	remoteIP := net.ParseIP(server)
	qm, err := sess.EstablishQuickMode(config.DefaultESPProposals, localIP, remoteIP)
	if err != nil {
		fmt.Println("QUICKMODE FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("QUICKMODE ESTABLISHED: in_spi=%08x out_spi=%08x enc_key_len=%d auth_key_len=%d\n",
		qm.Inbound.SPI, qm.Outbound.SPI, len(qm.Inbound.EncKey), len(qm.Inbound.AuthKey))
}
