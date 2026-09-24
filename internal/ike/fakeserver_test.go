package ike

import (
	"bytes"
	"crypto/rand"
	"net"
	"sync"
	"testing"
)

// fakeServer is an IKEv1 PSK Main Mode + Quick Mode responder on loopback,
// built from this package's own primitives, so the client's Phase 1 / Quick
// Mode flows — including a second Phase 1 next to a live one — run end to end
// in-process. It shares crypto code with the client, so it proves the message
// flow and state handling, not interoperability with a particular real server.
type fakeServer struct {
	t     *testing.T
	psk   []byte
	esp   []string
	main  *net.UDPConn // stands in for UDP/500
	natt  *net.UDPConn // stands in for UDP/4500
	newSA chan *QuickModeResult

	// authMayFail: the test expects the client to authenticate wrongly, so a
	// server that cannot make sense of MM5 stays silent (as a real one does)
	// instead of failing the test.
	authMayFail bool

	mu   sync.Mutex // serialises every handler: the two sockets are read by two goroutines
	sess map[[8]byte]*fakeSA
	done chan struct{}
}

type fakeSA struct {
	transform  Transform
	saBody     []byte
	ckyI, ckyR [8]byte
	kp         *KeyPair
	gxi, gxr   []byte
	ni, nr     []byte
	keys       *Phase1Keys
	awaitMM5   bool
	srv        *Session // server-side view of the established IKE SA
	clientNATT *net.UDPAddr
}

func newFakeServer(t *testing.T, psk string, esp []string) *fakeServer {
	t.Helper()
	lo := net.ParseIP("127.0.0.1")
	main, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	natt, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{t: t, psk: []byte(psk), esp: esp, main: main, natt: natt, newSA: make(chan *QuickModeResult, 16), sess: map[[8]byte]*fakeSA{}, done: make(chan struct{})}
	go f.serve(main, false)
	go f.serve(natt, true)
	t.Cleanup(func() { close(f.done); main.Close(); natt.Close() })
	return f
}

// expectAuthFailure tells the fake the client is about to authenticate
// wrongly (safe to call while the server goroutines run).
func (f *fakeServer) expectAuthFailure() {
	f.mu.Lock()
	f.authMayFail = true
	f.mu.Unlock()
}

func (f *fakeServer) authProblem(format string, args ...any) {
	if !f.authMayFail {
		f.t.Errorf("fake server: "+format, args...)
	}
}

func (f *fakeServer) mainPort() int { return f.main.LocalAddr().(*net.UDPAddr).Port }
func (f *fakeServer) nattPort() int { return f.natt.LocalAddr().(*net.UDPAddr).Port }

func (f *fakeServer) serve(conn *net.UDPConn, floated bool) {
	buf := make([]byte, 65535)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg := append([]byte(nil), buf[:n]...)
		if floated {
			if n < 4 || !bytes.Equal(msg[:4], nonESPMarker) {
				continue // ESP or a keepalive: not IKE
			}
			msg = msg[4:]
		}
		if len(msg) < headerLen {
			continue
		}
		h, err := ParseHeader(msg)
		if err != nil {
			continue
		}
		f.handle(conn, from, h, msg, floated)
	}
}

func (f *fakeServer) reply(conn *net.UDPConn, to *net.UDPAddr, msg []byte, floated bool) {
	if floated {
		msg = append(append([]byte{}, nonESPMarker...), msg...)
	}
	if _, err := conn.WriteToUDP(msg, to); err != nil {
		f.t.Logf("fake server send: %v", err)
	}
}

func (f *fakeServer) handle(conn *net.UDPConn, from *net.UDPAddr, h Header, msg []byte, floated bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sa := f.sess[h.InitiatorSPI]

	switch {
	case h.ExchangeType == ExchangeIdentityProt && sa == nil:
		f.mm1(conn, from, h, msg)
	case h.ExchangeType == ExchangeIdentityProt && sa != nil && !sa.awaitMM5:
		f.mm3(conn, from, h, msg, sa)
	case h.ExchangeType == ExchangeIdentityProt && sa != nil && sa.awaitMM5:
		f.mm5(conn, from, h, msg, sa)
	case sa != nil && sa.srv != nil:
		sa.srv.dispatchIKE(msg) // Quick Mode / Informational on an established IKE SA
	}
}

func (f *fakeServer) mm1(conn *net.UDPConn, from *net.UDPAddr, h Header, msg []byte) {
	payloads, err := SplitPayloads(h.NextPayload, msg[headerLen:])
	if err != nil {
		return
	}
	var saBody []byte
	for _, p := range payloads {
		if p.Type == PayloadSA {
			saBody = p.Body
			break
		}
	}
	tr, err := ParseChosenTransform(saBody) // the test offers exactly one transform
	if err != nil {
		f.t.Errorf("fake server: unusable MM1 SA: %v", err)
		return
	}
	sa := &fakeSA{transform: tr, saBody: saBody, ckyI: h.InitiatorSPI}
	rand.Read(sa.ckyR[:])
	f.sess[h.InitiatorSPI] = sa

	body := append(marshalPayload(PayloadVendorID, saBody), marshalPayload(PayloadNone, RFC3947VendorID())...)
	rh := Header{InitiatorSPI: sa.ckyI, ResponderSPI: sa.ckyR, NextPayload: PayloadSA, Version: 0x10, ExchangeType: ExchangeIdentityProt}
	rh.Length = uint32(headerLen + len(body))
	f.reply(conn, from, append(rh.Marshal(), body...), false)
}

func (f *fakeServer) mm3(conn *net.UDPConn, from *net.UDPAddr, h Header, msg []byte, sa *fakeSA) {
	payloads, err := SplitPayloads(h.NextPayload, msg[headerLen:])
	if err != nil {
		return
	}
	for _, p := range payloads {
		switch p.Type {
		case PayloadKE:
			sa.gxi = p.Body
		case PayloadNonce:
			sa.ni = p.Body
		}
	}
	group := Groups[sa.transform.Group]
	kp, err := GenerateKeyPair(group)
	if err != nil {
		f.t.Errorf("fake server DH: %v", err)
		return
	}
	sa.kp, sa.gxr = kp, kp.PublicBytes()
	sa.nr = make([]byte, 32)
	rand.Read(sa.nr)
	gxy, err := kp.SharedSecret(sa.gxi)
	if err != nil {
		f.t.Errorf("fake server shared secret: %v", err)
		return
	}
	sa.keys, err = DerivePhase1Keys(sa.transform, f.psk, sa.ni, sa.nr, gxy, sa.ckyI, sa.ckyR)
	if err != nil {
		f.t.Errorf("fake server keys: %v", err)
		return
	}
	// NAT-D hashes that match nothing: the client concludes there is a NAT and floats.
	junk, _ := digest(sa.transform.Hash, []byte("no such address"))
	body := marshalPayload(PayloadNonce, sa.gxr)
	body = append(body, marshalPayload(payloadNATD, sa.nr)...)
	body = append(body, marshalPayload(payloadNATD, junk)...)
	body = append(body, marshalPayload(PayloadNone, junk)...)
	rh := Header{InitiatorSPI: sa.ckyI, ResponderSPI: sa.ckyR, NextPayload: PayloadKE, Version: 0x10, ExchangeType: ExchangeIdentityProt}
	rh.Length = uint32(headerLen + len(body))
	sa.awaitMM5 = true // before replying: MM5 may arrive on the other socket immediately
	f.reply(conn, from, append(rh.Marshal(), body...), false)
}

func (f *fakeServer) mm5(conn *net.UDPConn, from *net.UDPAddr, h Header, msg []byte, sa *fakeSA) {
	bs := blockSize(sa.transform)
	ivSeed, _ := digest(sa.transform.Hash, append(append([]byte{}, sa.gxi...), sa.gxr...))
	enc := msg[headerLen:]
	plain, err := cbcDecrypt(sa.transform, sa.keys.EncKey, ivSeed[:bs], enc)
	if err != nil {
		f.authProblem("decrypt MM5: %v", err)
		return
	}
	payloads, err := SplitPayloads(h.NextPayload, plain)
	if err != nil {
		f.authProblem("MM5 payloads: %v", err)
		return
	}
	var idBody, hashI []byte
	for _, p := range payloads {
		switch p.Type {
		case PayloadID:
			idBody = p.Body
		case PayloadHash:
			hashI = p.Body
		}
	}
	want, _ := sa.keys.ComputeHashI(sa.gxi, sa.gxr, sa.ckyI, sa.ckyR, sa.saBody, idBody)
	if !bytes.Equal(want, hashI) {
		f.authProblem("HASH_I mismatch (client and server disagree on the keys)")
		return
	}

	idR := MarshalIPv4ID(net.ParseIP("127.0.0.1"))
	hashR, _ := sa.keys.ComputeHashR(sa.gxr, sa.gxi, sa.ckyR, sa.ckyI, sa.saBody, idR)
	plain6 := padToBlock(append(marshalPayload(PayloadHash, idR), marshalPayload(PayloadNone, hashR)...), bs)
	ct6, err := cbcEncrypt(sa.transform, sa.keys.EncKey, enc[len(enc)-bs:], plain6)
	if err != nil {
		f.t.Errorf("fake server: encrypt MM6: %v", err)
		return
	}
	rh := Header{InitiatorSPI: sa.ckyI, ResponderSPI: sa.ckyR, NextPayload: PayloadID, Version: 0x10, ExchangeType: ExchangeIdentityProt, Flags: FlagEncryption}
	rh.Length = uint32(headerLen + len(ct6))
	f.reply(conn, from, append(rh.Marshal(), ct6...), true)

	sa.awaitMM5 = false
	sa.clientNATT = from
	sa.srv = &Session{
		conn: f.natt, serverIP: net.ParseIP("127.0.0.1"), destAddr: from, floated: true,
		InitiatorSPI: sa.ckyI, ResponderSPI: sa.ckyR, Keys: sa.keys, Transform: sa.transform,
		phase1IV: append([]byte(nil), ct6[len(ct6)-bs:]...),
	}
	sa.srv.dp = &dataPlane{
		espIn: make(chan []byte, 8), done: make(chan struct{}),
		pending: map[uint32]chan []byte{}, resp: map[uint32]*respQM{},
		events: Events{ESPProposals: f.esp, NewChildSA: func(qm *QuickModeResult) { f.newSA <- qm }},
	}
}
