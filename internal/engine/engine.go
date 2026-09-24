// Package engine orchestrates a full VPN connection: diagnostics, IKE
// Phase 1/2, ESP, L2TP, PPP, and routing/DNS — the state machine the CLI's
// connect/disconnect/repair commands drive.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vpn/internal/dnsmgr"
	"vpn/internal/ike"
	"vpn/internal/ipsec"
	"vpn/internal/l2tp"
	"vpn/internal/ppp"
	"vpn/internal/privilege"
	"vpn/internal/routing"
	"vpn/internal/state"
	"vpn/internal/sysbin"
	"vpn/internal/tun"
	"vpn/internal/vpnlog"
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
	RekeyAfter   time.Duration // >0: rekey this often instead of at half the SA lifetime
}

// errAbortedByDisconnect is Connect's internal sentinel for "a `disconnect`
// arrived while still negotiating and this process cancelled itself in
// response" — not a real connection failure, so it must never surface as a
// FAILED state/alert to the UI.
var errAbortedByDisconnect = errors.New("connect aborted: disconnected while negotiating")

// Connect runs the full stage sequence and, on success, leaves the tunnel
// interface up, routes/DNS applied, and state.Save()'d as CONNECTED. On any
// failure, it restores whatever it had already changed before returning —
// a failed connect must never leave the machine half-configured.
//
// Privilege footprint: negotiation (IKE/L2TP/PPP — binding UDP/500, then
// parsing whatever the far end sends) and setup (opening utun, route/DNS
// changes) run under privilege.Elevate, since those genuinely need root on
// macOS. Once the tunnel is up, runDataPlane — the long-lived loop that
// continuously parses untrusted IP packets from the far end of the tunnel
// for as long as the VPN stays connected — runs at the caller's real,
// unprivileged UID: it only needs the already-open utun fd and ESP socket,
// neither of which requires root once open, so there's no reason for the
// process to keep holding root through what's normally the vast majority
// of a session's lifetime.
func Connect(cfg Config) error {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	// Registered before anything else — including before the PID that lets
	// `disconnect` find this process at all — and reused for the entire
	// call (setup negotiation *and* the data-plane phase below) instead of
	// two separate signal.NotifyContexts, so there is exactly one place
	// that decides whether a termination was requested.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	st := &state.State{Phase: state.PhaseConnecting, Profile: cfg.ProfileName, Account: cfg.AccountName, Server: cfg.Server}

	var rtSnapshot *routing.Snapshot
	var dnsSnap *dnsmgr.Snapshot
	var l2tpTun *l2tp.Tunnel
	var dev *tun.Device
	var pppT *pppOverL2TP
	var ipcp ppp.NegotiatedIPCP
	var startRekey func(ctx context.Context) // set once Quick Mode succeeds

	// The IKE/ESP socket reader (ike.StartDataPhase) runs from Quick Mode
	// until Connect returns, on every path.
	ikeCtx, stopIKE := context.WithCancel(sigCtx)
	defer stopIKE()

	setupErr := privilege.Elevate(func() error {
		// vpnlog.Init has to run here, not before Elevate: /var/log/vpn.log
		// is root-owned (see internal/vpnlog), and this is the one point in
		// Connect that's actually privileged. Once open, the fd stays valid
		// for every later vpnlog call in this process regardless of euid —
		// Unix only checks permissions at open(2), not on each write.
		if err := vpnlog.Init(cfg.Verbose); err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		// Record PID together with the very first save, not only once
		// CONNECTED (as before) — otherwise a `disconnect` issued while
		// this process is still negotiating IKE/L2TP/PPP finds state.PID
		// still zero, can't signal this process, and just clears the state
		// file locally while this process keeps running and authenticating
		// in the background. A `connect` started right after then races
		// that orphan for the same account, which is what intermittently
		// produced spurious PPP auth failures / stale "already logged in"
		// sessions, and later state-file flicker, on rapid on/off/on
		// toggling: the orphan can finish and overwrite state.json to
		// CONNECTED well after the UI already believes it disconnected.
		st.PID = os.Getpid()
		_ = st.Save()

		fail := func(stage, detail string, err error) error {
			if sigCtx.Err() != nil {
				// Cancelled by our own signal handler above, not a real
				// failure — whatever was captured/applied so far has
				// already been restored at each call site below, exactly
				// like any other failure path.
				_ = state.Clear()
				vpnlog.Info("ENGINE", "connect aborted by disconnect", vpnlog.Fields{"stage": stage})
				return errAbortedByDisconnect
			}
			st.Phase = state.PhaseFailed
			st.FailStage = stage
			// Include the real underlying error, not just the generic
			// stage label passed in `detail` ("PPP negotiation", "IKEv1
			// Phase 1 negotiation", ...). The UI's failure classification
			// (session-stale vs. wrong-credentials vs. IKE/route failure)
			// pattern-matches against this string for things like "already
			// logged in" or "CHAP authentication rejected" — without the
			// real text, every PPP-stage failure looked identical to the
			// UI regardless of whether the server actually said "wrong
			// password" or "you're already logged in", so the dedicated
			// stale-session auto-retry flow could never trigger.
			st.FailDetail = fmt.Sprintf("%s: %v", detail, err)
			_ = st.Save()
			vpnlog.Error(stage, detail, vpnlog.Fields{"err": err})
			return fmt.Errorf("%s: %s: %w", stage, detail, err)
		}

		var err error
		if err := ike.ValidateESPProposals(cfg.ESPProposals); err != nil {
			return fail("IPSEC_FAILURE", "invalid ESP proposal in profile", err)
		}
		rtSnapshot, err = routing.Capture()
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

		ctx, cancel := context.WithTimeout(sigCtx, cfg.Timeout)
		defer cancel()

		sess, err := ike.EstablishPhase1(ctx, ike.Config{
			ServerHost: serverIP.String(),
			ServerID:   cfg.ServerID,
			PSK:        cfg.PSK,
			Proposals:  cfg.IKEProposals,
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
		vpnlog.Info("ENGINE", "Quick Mode established", vpnlog.Fields{"in_spi": fmt.Sprintf("%08x", qm.Inbound.SPI), "out_spi": fmt.Sprintf("%08x", qm.Outbound.SPI)})

		sas, err := newSASet(qm)
		if err != nil {
			_ = rtSnapshot.Restore()
			return fail("IPSEC_FAILURE", "set up ESP SAs", err)
		}
		// From here on one goroutine owns the socket and demultiplexes ESP
		// from IKE — required for rekeying while traffic flows.
		sess.StartDataPhase(ikeCtx, ike.Events{
			DeleteESP: func(spis []uint32) {
				if sas.drop(spis) {
					vpnlog.Error("ENGINE", "server deleted the ESP SA currently in use — traffic stalls until the next rekey", nil)
				}
			},
		})
		startRekey = func(ctx context.Context) {
			go runRekey(ctx, sas, sess, func() (*ike.QuickModeResult, error) {
				return sess.RekeyQuickMode(cfg.ESPProposals, localIP, serverIP)
			}, qm.Lifetime, cfg.RekeyAfter)
		}
		espT := &espTransport{
			sess: sess,
			sas:  sas,
			repairRoute: func() error {
				return privilege.Elevate(func() error {
					return rtSnapshot.ProtectServer(serverIP.String())
				})
			},
		}

		hostName, _ := localHostName()
		l2tpTun, err = l2tp.Establish(ctx, espT, l2tp.Config{HostName: hostName, Timeout: cfg.Timeout})
		if err != nil {
			_ = rtSnapshot.Restore()
			return fail("L2TP_TIMEOUT", "L2TP tunnel/session establishment", err)
		}
		vpnlog.Info("ENGINE", "L2TP session established", nil)

		pppT = &pppOverL2TP{tun: l2tpTun}
		mru := uint16(cfg.MTU)
		if mru == 0 {
			mru = 1400
		}
		pppResult, err := ppp.Run(ctx, pppT, ppp.Config{MRU: mru, Username: cfg.AccountName, Password: cfg.Password, Timeout: cfg.Timeout})
		if err != nil {
			l2tpTun.Close()
			_ = rtSnapshot.Restore()
			return fail(pppFailStage(err), "PPP negotiation", err)
		}
		ipcp = pppResult.IPCP
		vpnlog.Info("ENGINE", "PPP OPENED", vpnlog.Fields{"local_ip": ipcp.LocalIP.String(), "peer_ip": ipcp.PeerIP.String()})

		// Everything below is teardown-on-failure just like the stages
		// above, but now there are more resources (utun device, interface
		// config, DNS) to unwind in reverse order if any step fails.
		dev, err = tun.Open()
		if err != nil {
			ppp.Terminate(pppT, 1)
			l2tpTun.Close()
			_ = rtSnapshot.Restore()
			return fail("TUN_FAILURE", "open utun device", err)
		}
		teardownPartial := func() {
			dev.Close()
			ppp.Terminate(pppT, 1)
			l2tpTun.Close()
			if dnsSnap != nil {
				_ = dnsSnap.Restore()
			}
			_ = rtSnapshot.Restore()
		}

		if ipcp.PeerIP == nil {
			teardownPartial()
			return fail("TUN_FAILURE", "configure utun interface", fmt.Errorf("LNS never sent its own IPCP IP-Address option — nothing to point the point-to-point link at"))
		}
		if err := routing.ConfigureP2PInterface(dev.Name, ipcp.LocalIP.String(), ipcp.PeerIP.String(), int(mru)); err != nil {
			teardownPartial()
			return fail("TUN_FAILURE", "configure utun interface", err)
		}
		vpnlog.Info("ENGINE", "utun interface up", vpnlog.Fields{"device": dev.Name, "local_ip": ipcp.LocalIP.String(), "peer_ip": ipcp.PeerIP.String()})

		if cfg.FullTunnel {
			if err := rtSnapshot.ApplyFullTunnel(dev.Name); err != nil {
				teardownPartial()
				return fail("ROUTE_FAILURE", "apply full-tunnel default routes", err)
			}
			// Adding split-default routes can make macOS discard the existing
			// cloned host route. Re-add the static exception before ESP sends.
			if err := rtSnapshot.ProtectServer(serverIP.String()); err != nil {
				teardownPartial()
				return fail("ROUTE_FAILURE", "protect VPN server after full-tunnel routes", err)
			}
			vpnlog.Info("ENGINE", "full-tunnel routes applied", vpnlog.Fields{"device": dev.Name})
		}

		dnsServers := dnsServerStrings(ipcp)
		if len(dnsServers) > 0 {
			service, err := dnsmgr.ServiceForInterface(rtSnapshot.DefaultInterface)
			if err != nil {
				teardownPartial()
				return fail("DNS_FAILURE", "map default interface to a network service", err)
			}
			snap, err := dnsmgr.Capture(service)
			if err != nil {
				teardownPartial()
				return fail("DNS_FAILURE", "capture current DNS configuration", err)
			}
			if err := snap.Apply(dnsServers); err != nil {
				teardownPartial()
				return fail("DNS_FAILURE", "apply LNS-provided DNS servers", err)
			}
			dnsSnap = snap
			st.DNSService = snap.Service
			st.DNSServers = snap.Servers
			st.DNSApplied = true
			vpnlog.Info("ENGINE", "DNS applied", vpnlog.Fields{"service": service, "servers": dnsServers})
		} else if cfg.FullTunnel {
			// Not an error — some LNSes simply don't push DNS — but the
			// system keeps its current resolvers, and one on the local
			// network (e.g. the Wi-Fi router) is reached via its on-link
			// route, outside the tunnel: every lookup is visible to that
			// network.
			st.Warnings = append(st.Warnings, warnNoPushedDNS)
			vpnlog.Error("ENGINE", warnNoPushedDNS, nil)
		}

		st.Phase = state.PhaseConnected
		// st.PID was already recorded at the top of this closure.
		st.TunDevice = dev.Name
		st.LocalIP = ipcp.LocalIP.String()
		st.SavedRoutes = true
		if err := st.Save(); err != nil {
			teardownPartial()
			return fail("STATE_FAILURE", "persist connected state", err)
		}
		return nil
	})
	if setupErr != nil {
		if errors.Is(setupErr, errAbortedByDisconnect) {
			// A deliberate, in-flight disconnect — already cleaned up and
			// cleared inside fail() above; report success to the caller
			// (the CLI's own `disconnect` is what's waiting on this).
			return nil
		}
		return setupErr
	}
	vpnlog.Info("ENGINE", "VPN connected", vpnlog.Fields{"local_ip": ipcp.LocalIP.String(), "device": dev.Name})

	// macOS can silently reap the routes ProtectServer/ApplyFullTunnel just
	// installed at any point during a long-lived session (see routing.go's
	// Watch doc comment) — re-assert them periodically instead of trusting
	// they stay in place for the whole connection.
	go rtSnapshot.Watch(sigCtx, privilege.Elevate, 10*time.Second)
	startRekey(sigCtx)

	pumpErr := runDataPlane(sigCtx, dev, pppT)

	// Tear down on the way out no matter why the pump stopped (signal or
	// error) — the disconnect/repair CLI paths exist for when this process
	// is no longer around to do it itself, not as the primary mechanism.
	return privilege.Elevate(func() error {
		dev.Close()
		ppp.Terminate(pppT, 1)
		l2tpTun.Close()
		if dnsSnap != nil {
			_ = dnsSnap.Restore()
		}
		_ = rtSnapshot.Restore()

		if pumpErr != nil && sigCtx.Err() == nil {
			// Stopped for a reason other than the signal we were waiting
			// for. The tunnel fails open (routes/DNS restored above), so
			// record that loudly for `vpn status` instead of looking like
			// an ordinary disconnect.
			failed := &state.State{
				Phase:      state.PhaseFailed,
				Profile:    cfg.ProfileName,
				Account:    cfg.AccountName,
				Server:     cfg.Server,
				FailStage:  "TUNNEL_FAILURE",
				FailDetail: fmt.Sprintf("tunnel dropped (%v) — traffic now goes over the normal network WITHOUT VPN protection; run `vpn connect` again", pumpErr),
			}
			_ = failed.Save()
			vpnlog.Error("ENGINE", "data plane stopped unexpectedly; traffic now bypasses the VPN", vpnlog.Fields{"err": pumpErr})
			return fmt.Errorf("TUNNEL_FAILURE: data plane stopped: %w", pumpErr)
		}
		_ = state.Clear()
		vpnlog.Info("ENGINE", "disconnected", nil)
		return nil
	})
}

// warnNoPushedDNS is surfaced by connect/status when the LNS assigned no DNS
// servers under full tunnel (see Connect).
const warnNoPushedDNS = "VPN server pushed no DNS servers: DNS lookups keep using this network's resolvers, and any on the local network bypass the VPN"

// newESPSA turns one direction of Quick Mode's result into the data-plane
// SA for exactly the transform that was negotiated.
func newESPSA(c ike.ChildSA) (*ipsec.SA, error) {
	var cipher ipsec.Cipher
	switch c.Transform.Encryption {
	case ike.Enc3DES:
		cipher = ipsec.Cipher3DESCBC
	case ike.EncAES:
		cipher = ipsec.CipherAESCBC
	default:
		return nil, fmt.Errorf("negotiated ESP encryption %d has no data-plane implementation", c.Transform.Encryption)
	}
	var integrity ipsec.Integrity
	switch c.Transform.Hash {
	case ike.HashSHA1:
		integrity = ipsec.IntegHMACSHA1_96
	case ike.HashSHA256:
		integrity = ipsec.IntegHMACSHA256_128
	default:
		return nil, fmt.Errorf("negotiated ESP integrity %d has no data-plane implementation", c.Transform.Hash)
	}
	return ipsec.NewSA(c.SPI, cipher, integrity, c.EncKey, c.AuthKey)
}

// dnsServerStrings collects the LNS-provided DNS servers as strings,
// skipping any that are absent or 0.0.0.0 (the LNS declining to supply
// one, encoded the same way a IPCP request placeholder is).
func dnsServerStrings(ipcp ppp.NegotiatedIPCP) []string {
	var out []string
	for _, ip := range []net.IP{ipcp.PrimaryDNS, ipcp.SecondDNS} {
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}

// runDataPlane bridges the utun device and the PPP/L2TP/ESP stack: every
// IP packet read from one side is written to the other. It blocks until
// ctx is cancelled or either direction hits a fatal error.
func runDataPlane(ctx context.Context, dev *tun.Device, pppT *pppOverL2TP) error {
	errCh := make(chan error, 2)

	// utun -> PPP -> L2TP -> ESP
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := dev.Read(buf)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("read utun: %w", err):
				default:
				}
				return
			}
			// Only IPv4 is negotiated (IPCP, never IPV6CP): framing any
			// other packet as ppp.ProtoIP would hand the LNS garbage.
			if n == 0 || buf[0]>>4 != 4 {
				continue
			}
			if err := pppT.SendFrame(ppp.ProtoIP, buf[:n]); err != nil {
				select {
				case errCh <- fmt.Errorf("send PPP frame: %w", err):
				default:
				}
				return
			}
		}
	}()

	// ESP -> L2TP -> PPP -> utun
	go func() {
		for {
			proto, payload, err := pppT.RecvFrame(ctx)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("recv PPP frame: %w", err):
				default:
				}
				return
			}
			switch proto {
			case ppp.ProtoIP:
				if _, err := dev.Write(payload); err != nil {
					select {
					case errCh <- fmt.Errorf("write utun: %w", err):
					default:
					}
					return
				}
			case ppp.ProtoLCP:
				// The LNS may keep sending LCP Echo-Requests as a keepalive
				// during the data phase (negotiatePhase only handles these
				// while a Configure-Request/-Ack exchange is in flight) —
				// reply so it doesn't conclude the link died and tear the
				// session down from its side. It can also retransmit its
				// own Configure-Request here if our earlier Ack to it never
				// arrived (see runAuth's identical handling, which fixed a
				// live case of this stalling the peer's own LCP state
				// machine before it would even send a CHAP Challenge) — ack
				// it here too rather than assuming that can only happen
				// during auth.
				pkt, err := ppp.ParseControlPacket(payload)
				if err == nil {
					switch pkt.Code {
					case ppp.CodeEchoRequest:
						reply := ppp.ControlPacket{Code: ppp.CodeEchoReply, Identifier: pkt.Identifier, Data: pkt.Data}
						_ = pppT.SendFrame(ppp.ProtoLCP, reply.Marshal())
					case ppp.CodeConfigureRequest:
						reply := ppp.ControlPacket{Code: ppp.CodeConfigureAck, Identifier: pkt.Identifier, Data: pkt.Data}
						_ = pppT.SendFrame(ppp.ProtoLCP, reply.Marshal())
					}
				}
			}
			// Other protocols (e.g. IPV6CP, which this client never
			// negotiates) are silently ignored — the peer already knows we
			// didn't ack them.
		}
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
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

// connectLockPath is an flock'd file that serializes connect/disconnect's
// session-transition critical sections *across processes* — `connect` and
// `disconnect` are always separate OS processes (privilege.Elevate's own
// mutex only serializes goroutines within one process), so without this,
// a `disconnect` invoked right after a `connect` could run concurrently
// with that connect's own fork-and-record-PID step and simply find
// nothing yet to kill (see ClaimNewConnect's doc comment).
func connectLockPath() string { return filepath.Join(state.Dir, "connect.lock") }

// WithConnectLock runs fn with the cross-process connect lock held. Held
// only around the *transition* (deciding what's currently running, and
// killing/claiming it) — never around an entire connection attempt, so a
// disconnect can still promptly interrupt a slow negotiation once that
// negotiation's own claim has been recorded and this lock released.
func WithConnectLock(fn func() error) error {
	f, err := openLockFile()
	if err != nil {
		return fmt.Errorf("open connect lock: %w", err)
	}
	defer f.Close()
	// flock, not privilege.Elevate's mutex: this needs to serialize across
	// separate `vpn` processes, which an in-process mutex cannot do. Once
	// open, flock/close need no privilege regardless of who opened the fd
	// (permissions are only checked at open(2) — same reasoning as
	// vpnlog.Init's comment).
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire connect lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func openLockFile() (*os.File, error) {
	var f *os.File
	err := privilege.Elevate(func() error {
		if err := os.MkdirAll(state.Dir, 0o755); err != nil {
			return err
		}
		var err error
		f, err = os.OpenFile(connectLockPath(), os.O_CREATE|os.O_RDWR, 0o644)
		return err
	})
	return f, err
}

// Disconnect tears down a running connection. `connect` runs in the
// foreground (or daemonized) for as long as the tunnel is up — see its
// runDataPlane loop — so the normal path here is to signal that live
// process and let it do its own graceful teardown (closing the utun
// device, restoring routes/DNS, clearing state) exactly like a Ctrl-C
// would. Only if that process is gone (crashed, killed -9) does this fall
// back to redoing the restore itself from what was last recorded on disk.
func Disconnect() error {
	return WithConnectLock(func() error { return killExisting(true) })
}

// PrepareNewConnect tears down any previous connect/connected session
// before a new one starts. `connect` calls this itself while holding
// WithConnectLock (see cli.cmdConnect), before forking its daemon, so two
// negotiation attempts for the same account can never run concurrently.
// That used to be the caller's job (the menu bar app ran its own
// `disconnect` immediately before every `connect`, followed by an
// unconditional `pkill -9` as a blanket safety net); doing it here
// instead, synchronously and under the same lock the new daemon's PID
// gets claimed under, closes two gaps that approach had: a caller could
// race this process's own state.json write on a very fast reconnect, and
// SIGKILL bypassed graceful cancellation even for a process that would
// have responded to SIGTERM within milliseconds — which could catch it
// mid-PPP-authentication and leave a session the server still considers
// logged in, exactly what produced the intermittent "already logged in" /
// CHAP-rejected failures on rapid on/off/on toggling. Silent when there is
// nothing to tear down. Callers must already hold WithConnectLock.
func PrepareNewConnect() error {
	return killExisting(false)
}

// ClaimNewConnect blocks (briefly) until state.json shows childPID as a
// live CONNECTING session — i.e. the just-forked daemon has recorded
// itself — so the caller can safely release WithConnectLock knowing a
// disconnect issued right after this returns will always find and signal
// it. Without this confirmation, releasing the lock immediately after
// forking would reopen the exact race this lock exists to close: a
// disconnect could run in the gap between the fork and the child actually
// writing its PID, see nothing to kill, and leave the child running
// unnoticed. Callers must already hold WithConnectLock.
func ClaimNewConnect(childPID int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := state.Load()
		if err == nil && st.Phase == state.PhaseConnecting && st.PID == childPID {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("connect process (pid %d) did not report itself within %s", childPID, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// killExisting is disconnect/PrepareNewConnect's shared implementation.
// verbose controls the user-facing progress prints — off when `connect`
// calls this on its own behalf, so it doesn't narrate a disconnect the
// user never asked for. Callers must already hold WithConnectLock.
func killExisting(verbose bool) error {
	st, err := state.Load()
	if err != nil {
		return err
	}
	if st.Phase != state.PhaseConnected && st.Phase != state.PhaseConnecting {
		if verbose {
			fmt.Println("Already disconnected.")
		}
		return nil
	}
	return privilege.Elevate(func() error {
		if st.PID > 0 && processAlive(st.PID) {
			if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal running connect process (pid %d): %w", st.PID, err)
			}
			for i := 0; i < 50; i++ { // up to ~5s for its own graceful teardown
				time.Sleep(100 * time.Millisecond)
				cur, err := state.Load()
				if err == nil && cur.Phase == state.PhaseDisconnected {
					if verbose {
						fmt.Println("Disconnected.")
					}
					return nil
				}
			}
			// Unresponsive to SIGTERM for a full 5s — genuinely wedged,
			// not merely mid-negotiation (Connect's signal handler reacts
			// to SIGTERM within a retry-loop tick, well under this).
			// Escalate to SIGKILL of this exact PID — never a wildcard
			// process-name match — so a stuck connection can still always
			// be cleared without any risk of hitting an unrelated process
			// that happens to share `vpn connect` in its command line.
			vpnlog.Error("ENGINE", "connect process unresponsive to SIGTERM, escalating to SIGKILL", vpnlog.Fields{"pid": st.PID})
			_ = syscall.Kill(st.PID, syscall.SIGKILL)
			restoreRecorded(st)
			if verbose {
				fmt.Println("Force-terminated an unresponsive connection.")
			}
			return state.Clear()
		}
		// No live process to signal — restore from what was last recorded.
		restoreRecorded(st)
		return state.Clear()
	})
}

// Repair restores routing/DNS state after an abnormal termination, without
// needing credentials — it only ever removes state this client could have
// added (the two split-default override routes, the host route to
// whatever server address was last recorded, and the DNS servers it
// pushed). It refuses to act while a `connect` process is still alive —
// use `disconnect` for that, which lets that process tear itself down
// instead of racing it.
func Repair() error {
	st, err := state.Load()
	if err != nil {
		return err
	}
	if st.PID > 0 && processAlive(st.PID) {
		return fmt.Errorf("connect (pid %d) is still running — use `vpn disconnect` instead", st.PID)
	}
	return privilege.Elevate(func() error {
		if st.Server == "" {
			fmt.Println("No recorded server to repair routes for — nothing to do.")
			return state.Clear()
		}
		restoreRecorded(st)
		fmt.Printf("Restored routing/DNS state for %s.\n", st.Server)
		return state.Clear()
	})
}

// restoreRecorded redoes routing.Restore/dnsmgr.Restore purely from what
// was last persisted to disk — the only option once the process that held
// the live *routing.Snapshot/*dnsmgr.Snapshot objects is no longer around.
func restoreRecorded(st *state.State) {
	if st.Server != "" {
		_ = routing.RestoreByServerIP(st.Server)
	}
	if st.DNSApplied && st.DNSService != "" {
		_ = dnsmgr.FromRecorded(st.DNSService, st.DNSServers, true).Restore()
	}
}

// processAlive reports whether pid names a live process this user can
// signal — sending signal 0 performs the existence/permission check
// without actually delivering anything (man 2 kill).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
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
	out, err := exec.Command(sysbin.Ifconfig, iface).Output()
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
