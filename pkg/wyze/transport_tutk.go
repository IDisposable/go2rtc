package wyze

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
)

// tutkTransport drives a non-DTLS ("TransCode" firmware) Wyze camera over the
// plain TUTK P2P link. It carries the same Wyze HL K-command protocol as the
// DTLS path, but the AV login is handled by tutk.Dial and HL commands ride
// inside TUTK IOCtrl messages instead of DTLS records.
//
// RECONSTRUCTED FROM PUBLIC SOURCES — three things must be confirmed against a
// real non-DTLS camera (and docker-wyze-bridge's tutk_protocol.py):
//
//  1. tutkCredUser / the password used for tutk.Dial (AV login).
//  2. ioctrlTypeHL: the IOCtrl ctrl_type that wraps an HL K-command. The HL
//     header already carries the K-number in its cmdID field, so this is most
//     likely a single fixed transport type rather than per-command.
//  3. Whether older firmware speaks HL K-commands at all (some may use the
//     legacy avSendIOCtrl JSON command set, like the Xiaomi path).
//
// Two-way audio (intercom) over non-DTLS is intentionally disabled until the
// backchannel framing is confirmed; video + listen-only audio are the goal.
type tutkTransport struct {
	conn *tutk.Conn

	videoTS tsAccum
	audioTS tsAccum

	closeMu sync.Mutex
	closed  bool
}

const (
	// tutkCredUser is the AV-login username. The DTLS path uses "admin" with
	// the ENR as the password; the non-DTLS path is assumed to match.
	tutkCredUser = "admin"

	// ioctrlTypeHL is the TUTK IOCtrl ctrl_type that carries an HL K-command.
	// TODO(verify): confirm against tutk_protocol.py / a capture. This value is
	// a placeholder — non-DTLS auth will not succeed until it is correct.
	ioctrlTypeHL uint32 = 0x06
)

func dialTUTK(host, uid, enr string) (*tutkTransport, error) {
	conn, err := tutk.Dial(host, uid, tutkCredUser, enr)
	if err != nil {
		return nil, fmt.Errorf("wyze: tutk dial failed: %w", err)
	}
	return &tutkTransport{conn: conn}, nil
}

// AVClientStart is a no-op: tutk.Dial already performed the AV-login handshake.
func (t *tutkTransport) AVClientStart(time.Duration) error { return nil }

func (t *tutkTransport) WriteIOCtrl(payload []byte) error {
	return t.conn.WriteCommand(ioctrlTypeHL, payload)
}

func (t *tutkTransport) WriteAndWaitIOCtrl(payload []byte, match func([]byte) bool, timeout time.Duration) ([]byte, error) {
	if err := t.conn.WriteCommand(ioctrlTypeHL, payload); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, data, err := t.conn.ReadCommand()
		if err != nil {
			return nil, err
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
		return nil, nil // skip frames we can't interpret
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
