// Package engine orchestrates a full VPN connection: diagnostics, IKE
// Phase 1/2, ESP, L2TP, PPP, and routing/DNS — the state machine the CLI's
// connect/disconnect/repair commands drive.
package engine

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/ninhlee99/vpn-l2tp/internal/ike"
	"github.com/ninhlee99/vpn-l2tp/internal/ipsec"
	"github.com/ninhlee99/vpn-l2tp/internal/l2tp"
	"github.com/ninhlee99/vpn-l2tp/internal/ppp"
	"github.com/ninhlee99/vpn-l2tp/internal/routing"
	"github.com/ninhlee99/vpn-l2tp/internal/state"
	"github.com/ninhlee99/vpn-l2tp/internal/vpnlog"
)

// LogPath returns where `logs`/`connect --verbose` write to.
func LogPath() string { return vpnlog.Path }

// Config is everything one connect attempt needs — assembled by the CLI
// from a Profile+Account+Keychain secrets, never persisted itself.
type Config struct {
	ProfileName  string
	Server       string
	ServerID     string
	AccountName  string
	Password     string
	PSK          string
	IKEProposals []string
	ESPProposals []string
	MTU          int
	FullTunnel   bool
	Timeout      time.Duration
	Verbose      bool
}

// Connect runs the full stage sequence and, on success, leaves the tunnel
// interface up, routes/DNS applied, and state.Save()'d as CONNECTED. On any
// failure, it restores whatever it had already changed before returning —
// a failed connect must never leave the machine half-configured.
func Connect(cfg Config) error {
	if err := vpnlog.Init(cfg.Verbose); err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	st := &state.State{Phase: state.PhaseConnecting, Profile: cfg.ProfileName, Account: cfg.AccountName, Server: cfg.Server}
	_ = st.Save()

	fail := func(stage, detail string, err error) error {
		st.Phase = state.PhaseFailed
		st.FailStage = stage
		st.FailDetail = detail
		_ = st.Save()
		vpnlog.Error(stage, detail, vpnlog.Fields{"err": err})
		return fmt.Errorf("%s: %s: %w", stage, detail, err)
	}

	rtSnapshot, err := routing.Capture()
	if err != nil {
		return fail("ROUTE_FAILURE", "capture current routing state", err)
	}

	serverIP, err := resolveServer(cfg.Server)
	if err != nil {
		return fail("DNS_FAILURE", "resolve VPN server", err)
	}
	if err := rtSnapshot.ProtectServer(serverIP.String()); err != nil {
		return fail("ROUTE_FAILURE", "protect VPN server route", err)
	}

	localIP := localOutboundIP(rtSnapshot.DefaultInterface)
	if localIP == nil {
		return fail("ROUTE_FAILURE", "determine local outbound IP", fmt.Errorf("no IPv4 on %s", rtSnapshot.DefaultInterface))
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	ikeProposals := cfg.IKEProposals
	sess, err := ike.EstablishPhase1(ctx, ike.Config{
		ServerHost: serverIP.String(),
		ServerID:   cfg.ServerID,
		PSK:        cfg.PSK,
		Proposals:  ikeProposals,
		LocalIP:    localIP,
	})
	if err != nil {
		_ = rtSnapshot.Restore()
		return fail("IKE_TIMEOUT", "IKEv1 Phase 1 negotiation", err)
	}
	vpnlog.Info("ENGINE", "IKE Phase 1 established", vpnlog.Fields{"nat_detected": sess.NATDetected})

	qm, err := sess.EstablishQuickMode(cfg.ESPProposals, localIP, serverIP)
	if err != nil {
		_ = rtSnapshot.Restore()
		return fail("IPSEC_FAILURE", "Quick Mode (ESP SA) negotiation", err)
	}
	vpnlog.Info("ENGINE", "Quick Mode established", vpnlog.Fields{"in_spi": qm.Inbound.SPI, "out_spi": qm.Outbound.SPI})

	espT := &espTransport{
		sess: sess,
		out:  &ipsec.SA{SPI: qm.Outbound.SPI, EncKey: qm.Outbound.EncKey, AuthKey: qm.Outbound.AuthKey},
		in:   &ipsec.SA{SPI: qm.Inbound.SPI, EncKey: qm.Inbound.EncKey, AuthKey: qm.Inbound.AuthKey},
	}

	hostName, _ := localHostName()
	tun, err := l2tp.Establish(ctx, espT, l2tp.Config{HostName: hostName, Timeout: cfg.Timeout})
	if err != nil {
		_ = rtSnapshot.Restore()
		return fail("L2TP_TIMEOUT", "L2TP tunnel/session establishment", err)
	}
	vpnlog.Info("ENGINE", "L2TP session established", nil)

	pppT := &pppOverL2TP{tun: tun}
	mru := uint16(cfg.MTU)
	if mru == 0 {
		mru = 1400
	}
	pppResult, err := ppp.Run(ctx, pppT, ppp.Config{MRU: mru, Username: cfg.AccountName, Password: cfg.Password, Timeout: cfg.Timeout})
	if err != nil {
		tun.Close()
		_ = rtSnapshot.Restore()
		return fail(pppFailStage(err), "PPP negotiation", err)
	}
	vpnlog.Info("ENGINE", "PPP OPENED", vpnlog.Fields{"local_ip": pppResult.IPCP.LocalIP.String()})

	// The utun packet-forwarding loop (bridging the kernel interface to this
	// PPP/L2TP/ESP stack) and route/DNS application are not yet wired in —
	// report exactly how far this attempt got instead of claiming a usable
	// tunnel that isn't actually routing traffic yet.
	tun.Close()
	_ = rtSnapshot.Restore()
	return fail("NOT_YET_IMPLEMENTED", fmt.Sprintf("PPP established (local IP %s); utun packet forwarding not yet wired in", pppResult.IPCP.LocalIP), fmt.Errorf("in progress"))
}

func pppFailStage(err error) string {
	msg := err.Error()
	for _, stage := range []string{"LCP_FAILED", "PPP_AUTH_FAILURE", "IPCP_FAILURE"} {
		if len(msg) >= len(stage) && msg[:len(stage)] == stage {
			return stage
		}
	}
	return "LCP_FAILED"
}

func localHostName() (string, error) {
	return "l2tp-cli", nil
}

// Disconnect tears down a running connection cleanly, per the saved state.
func Disconnect() error {
	st, err := state.Load()
	if err != nil {
		return err
	}
	if st.Phase != state.PhaseConnected && st.Phase != state.PhaseConnecting {
		fmt.Println("Already disconnected.")
		return nil
	}
	if st.Server != "" {
		_ = routing.RestoreByServerIP(st.Server)
	}
	return state.Clear()
}

// Repair restores routing/DNS state after an abnormal termination, without
// needing credentials — it only ever removes state this client could have
// added (the two split-default override routes, and a host route to
// whatever server address was last recorded).
func Repair() error {
	st, err := state.Load()
	if err != nil {
		return err
	}
	if st.Server == "" {
		fmt.Println("No recorded server to repair routes for — nothing to do.")
		return state.Clear()
	}
	if err := routing.RestoreByServerIP(st.Server); err != nil {
		return err
	}
	fmt.Printf("Restored routing state for %s.\n", st.Server)
	return state.Clear()
}

func resolveServer(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
	}
	return ips[0], nil
}

func localOutboundIP(iface string) net.IP {
	out, err := exec.Command("ifconfig", iface).Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return net.ParseIP(fields[1])
			}
		}
	}
	return nil
}
