package wyze

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
)

// tutkTransport drives a non-DTLS ("TransCode" firmware) Wyze camera over the
// plain TUTK P2P link. It carries the same Wyze HL K-command protocol as the
// DTLS path, but the AV login is handled by tutk.Dial and HL commands ride
// inside TUTK IOCtrl messages instead of DTLS records.
//
// Unverified, confirm against a non-DTLS camera + tutk_protocol.py:
//   - tutkCredUser / password for tutk.Dial (AV login).
//   - HL ctrl_type (defaultHLType, override via URL hltype). Verbose mode logs
//     every incoming ctrl_type to reveal the right value.
//   - whether older firmware speaks HL at all vs legacy avSendIOCtrl JSON.
//
// Intercom is disabled here; video + listen-only audio only.
type tutkTransport struct {
	conn    *tutk.Conn
	hlType  uint32
	verbose bool

	videoTS tsAccum
	audioTS tsAccum

	closeMu sync.Mutex
	closed  bool
}

// tutkCredUser is the AV-login username. The DTLS path uses "admin" with the
// ENR as the password; the non-DTLS path is assumed to match.
// TODO(verify) against tutk_protocol.py / a V3 capture.
const tutkCredUser = "admin"

// defaultHLType is the placeholder IOCtrl ctrl_type wrapping an HL K-command.
// Override per-camera with the URL hltype param. TODO(verify).
const defaultHLType uint32 = 0x06

func parseHLType(s string) uint32 {
	if s == "" {
		return defaultHLType
	}
	if v, err := strconv.ParseUint(s, 0, 32); err == nil {
		return uint32(v)
	}
	return defaultHLType
}

func dialTUTK(host, uid, enr string, hlType uint32, verbose bool) (*tutkTransport, error) {
	if verbose {
		fmt.Printf("[Wyze/tutk] dial %s uid=%s user=%s hltype=0x%x\n", host, uid, tutkCredUser, hlType)
	}
	conn, err := tutk.Dial(host, uid, tutkCredUser, enr)
	if err != nil {
		return nil, fmt.Errorf("wyze: tutk dial failed: %w", err)
	}
	if verbose {
		fmt.Printf("[Wyze/tutk] connected %s\n", conn.Version())
	}
	return &tutkTransport{conn: conn, hlType: hlType, verbose: verbose}, nil
}

// AVClientStart is a no-op: tutk.Dial already performed the AV-login handshake.
func (t *tutkTransport) AVClientStart(time.Duration) error { return nil }

func (t *tutkTransport) WriteIOCtrl(payload []byte) error {
	return t.conn.WriteCommand(t.hlType, payload)
}

func (t *tutkTransport) WriteAndWaitIOCtrl(payload []byte, match func([]byte) bool, timeout time.Duration) ([]byte, error) {
	if cmdID, _, ok := tutk.ParseHL(payload); ok && t.verbose {
		fmt.Printf("[Wyze/tutk] -> ctrl_type=0x%x HL cmd=%d\n", t.hlType, cmdID)
	}
	if err := t.conn.WriteCommand(t.hlType, payload); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctrlType, data, err := t.conn.ReadCommand()
		if err != nil {
			return nil, err
		}
		// Log every incoming ctrl_type to reveal what the camera uses for HL.
		if t.verbose {
			cmdID, _, _ := tutk.ParseHL(data)
			fmt.Printf("[Wyze/tutk] <- ctrl_type=0x%x len=%d HL cmd=%d\n", ctrlType, len(data), cmdID)
		}
		// ReadCommand strips the 4-byte ctrl_type, so data starts at the HL
		// header. match (Client.matchHL) finds 'HL' from offset 0.
		if match(data) {
			return data, nil
		}
	}
	return nil, fmt.Errorf("wyze: timeout waiting for HL response")
}

func (t *tutkTransport) AVRecvFrameData() (*tutk.Packet, error) {
	hdr, payload, err := t.conn.ReadPacket()
	if err != nil {
		return nil, err
	}

	// The session layer already reassembled the frame; hdr ends with the
	// 40-byte Wyze FrameInfo trailer.
	fi := tutk.ParseFrameInfo(hdr)
	if fi == nil || len(payload) == 0 {
		return nil, nil // skip uninterpretable frames
	}

	pkt := &tutk.Packet{
		Codec:      fi.CodecID,
		Payload:    payload,
		IsKeyframe: fi.IsKeyframe(),
		FrameNo:    fi.FrameNo,
	}

	if tutk.IsAudioCodec(fi.CodecID) {
		pkt.Channel = tutk.ChannelAudio
		pkt.SampleRate = fi.SampleRate()
		pkt.Channels = fi.Channels()
		accumUS := t.audioTS.update(fi.Timestamp)
		pkt.Timestamp = uint32(accumUS * uint64(pkt.SampleRate) / 1_000_000)
	} else {
		pkt.Channel = tutk.ChannelIVideo
		accumUS := t.videoTS.update(fi.Timestamp)
		pkt.Timestamp = uint32(accumUS * 90000 / 1_000_000)
	}

	return pkt, nil
}

// --- Backchannel: not yet supported over non-DTLS ---

func (t *tutkTransport) AVSendAudioData(byte, []byte, uint32, uint32, uint8) error {
	return fmt.Errorf("wyze: intercom not supported on non-DTLS cameras")
}
func (t *tutkTransport) AVServStart() error {
	return fmt.Errorf("wyze: intercom not supported on non-DTLS cameras")
}
func (t *tutkTransport) AVServStop() error        { return nil }
func (t *tutkTransport) IsBackchannelReady() bool { return false }
func (t *tutkTransport) HasTwoWayStreaming() bool { return false }

func (t *tutkTransport) RemoteAddr() net.Addr           { return t.conn.RemoteAddr() }
func (t *tutkTransport) SetDeadline(t2 time.Time) error { return t.conn.SetDeadline(t2) }

func (t *tutkTransport) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.conn.Close()
}

// tsAccum reconstructs a monotonic microsecond clock from the camera's raw
// per-frame timestamp. It mirrors the tracker in pkg/tutk used by the DTLS
// FrameHandler; kept local to avoid exporting that internal type.
type tsAccum struct {
	lastRaw uint32
	accumUS uint64
	started bool
}

func (a *tsAccum) update(rawTS uint32) uint64 {
	if !a.started {
		a.started = true
		a.lastRaw = rawTS
		return 0
	}
	var delta uint32
	if rawTS >= a.lastRaw {
		delta = rawTS - a.lastRaw
	} else {
		delta = (1_000_000 - a.lastRaw) + rawTS
	}
	if delta > 5_000_000 {
		delta = 0 // implausible jump; see tsTracker note in pkg/tutk/frame.go
	}
	a.accumUS += uint64(delta)
	a.lastRaw = rawTS
	return a.accumUS
}
