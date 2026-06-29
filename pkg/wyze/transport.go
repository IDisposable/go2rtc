package wyze

import (
	"net"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
)

// transport is the camera link that wyze.Client drives. It is satisfied by the
// DTLS connection (*dtls.DTLSConn) for DTLS-firmware cameras and by the plain
// TUTK connection (*tutkTransport) for older TransCode-firmware cameras. Both
// carry the same Wyze "HL" K-command protocol; only the framing/encryption
// underneath differs.
type transport interface {
	// AVClientStart performs the AV-login handshake.
	AVClientStart(timeout time.Duration) error

	// WriteAndWaitIOCtrl sends an HL command and blocks until a response
	// matching match arrives (or timeout). Returns the raw response envelope.
	WriteAndWaitIOCtrl(payload []byte, match func([]byte) bool, timeout time.Duration) ([]byte, error)

	// WriteIOCtrl sends an HL command fire-and-forget.
	WriteIOCtrl(payload []byte) error

	// AVRecvFrameData returns the next assembled audio/video frame.
	AVRecvFrameData() (*tutk.Packet, error)

	// AVSendAudioData sends one backchannel (intercom) audio frame.
	AVSendAudioData(codec byte, payload []byte, timestampUS uint32, sampleRate uint32, channels uint8) error

	// AVServStart / AVServStop manage the return-audio (speaker) channel.
	AVServStart() error
	AVServStop() error
	IsBackchannelReady() bool

	// HasTwoWayStreaming reports whether the camera advertised intercom support.
	HasTwoWayStreaming() bool

	RemoteAddr() net.Addr
	SetDeadline(t time.Time) error
	Close() error
}
