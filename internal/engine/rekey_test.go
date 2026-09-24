package engine

import (
	"testing"
	"time"

	"vpn/internal/ike"
)

func testQM(in, out uint32, life time.Duration) *ike.QuickModeResult {
	tr := ike.Transform{Encryption: ike.Enc3DES, Hash: ike.HashSHA1}
	sa := func(spi uint32) ike.ChildSA {
		return ike.ChildSA{SPI: spi, EncKey: make([]byte, 24), AuthKey: make([]byte, 20), Transform: tr}
	}
	return &ike.QuickModeResult{Inbound: sa(in), Outbound: sa(out), Lifetime: life}
}

func TestSASetRekeyKeepsOldInboundUntilDeleted(t *testing.T) {
	now := time.Now()
	sas, err := newSASet(testQM(0x1, 0xA, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := sas.install(testQM(0x2, 0xB, time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if got := sas.current().out.SPI; got != 0xB {
		t.Fatalf("sending on %x, want the new pair's b", got)
	}
	if sas.inbound(0x1) == nil || sas.inbound(0x2) == nil {
		t.Fatal("both old and new inbound SAs must be accepted right after a rekey")
	}

	// The server deletes the old pair, naming its own inbound (our old out).
	if sas.drop([]uint32{0xA}) {
		t.Fatal("dropping the old pair reported the current one")
	}
	if sas.inbound(0x1) != nil {
		t.Fatal("deleted pair still accepted")
	}
	if sas.inbound(0x2) == nil {
		t.Fatal("current pair lost")
	}
}

func TestSASetNeverDropsCurrent(t *testing.T) {
	sas, _ := newSASet(testQM(0x1, 0xA, time.Hour))
	if !sas.drop([]uint32{0x1}) {
		t.Fatal("deleting the current pair was not reported")
	}
	if sas.current().out.SPI != 0xA {
		t.Fatal("current pair removed — nothing left to send on")
	}
}

func TestSASetExpireOld(t *testing.T) {
	start := time.Now()
	sas, _ := newSASet(testQM(0x1, 0xA, time.Minute))
	sas.install(testQM(0x2, 0xB, time.Hour), start)
	sas.expireOld(start.Add(2 * time.Minute))
	if sas.inbound(0x1) != nil {
		t.Fatal("expired pair still accepted")
	}
	sas.expireOld(start.Add(48 * time.Hour))
	if sas.inbound(0x2) == nil {
		t.Fatal("current pair expired away — it must stay until replaced")
	}
}

func TestRekeyDelay(t *testing.T) {
	if got := rekeyDelay(time.Hour); got != 30*time.Minute {
		t.Fatalf("rekeyDelay(1h) = %s, want 30m", got)
	}
	if got := rekeyDelay(10 * time.Second); got != minRekeyWait {
		t.Fatalf("rekeyDelay(10s) = %s, want the %s floor", got, minRekeyWait)
	}
}
