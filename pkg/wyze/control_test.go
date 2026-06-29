package wyze

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
	"github.com/stretchr/testify/require"
)

// recordingConn is a transport mock that captures the last IOCtrl write and can
// optionally block WriteAndWaitIOCtrl to exercise per-opcode serialization.
type recordingConn struct {
	lastWrite []byte
	started   chan struct{} // signalled when a wait begins (nil = don't block)
	release   chan struct{} // wait returns after this closes
}

func (r *recordingConn) WriteIOCtrl(payload []byte) error {
	r.lastWrite = payload
	return nil
}

func (r *recordingConn) WriteAndWaitIOCtrl(payload []byte, _ func([]byte) bool, _ time.Duration) ([]byte, error) {
	r.lastWrite = payload
	if r.started != nil {
		r.started <- struct{}{}
		<-r.release
	}
	return nil, nil
}

func (r *recordingConn) AVClientStart(time.Duration) error                         { return nil }
func (r *recordingConn) AVRecvFrameData() (*tutk.Packet, error)                    { return nil, nil }
func (r *recordingConn) AVSendAudioData(byte, []byte, uint32, uint32, uint8) error { return nil }
func (r *recordingConn) AVServStart() error                                        { return nil }
func (r *recordingConn) AVServStop() error                                         { return nil }
func (r *recordingConn) IsBackchannelReady() bool                                  { return false }
func (r *recordingConn) HasTwoWayStreaming() bool                                  { return false }
func (r *recordingConn) RemoteAddr() net.Addr                                      { return nil }
func (r *recordingConn) SetDeadline(time.Time) error                               { return nil }
func (r *recordingConn) Close() error                                              { return nil }

// TestControlPayloads pins each command's opcode and payload bytes. Opcodes are
// the ground-truth values cross-referenced against wyzecam/tutk_protocol.py.
func TestControlPayloads(t *testing.T) {
	tests := []struct {
		name    string
		call    func(*Client) error
		code    uint16
		payload []byte
	}{
		{"night_vision", func(c *Client) error { return c.SetNightVision(3) }, KCmdSetNightVision, []byte{3}},
		{"ir_led", func(c *Client) error { return c.SetIRLED(2) }, KCmdSetIRLED, []byte{2}},
		{"spotlight_on", func(c *Client) error { return c.SetSpotlight(true) }, KCmdSetSpotlight, []byte{1}},
		{"spotlight_off", func(c *Client) error { return c.SetSpotlight(false) }, KCmdSetSpotlight, []byte{2}},
		{"siren", func(c *Client) error { return c.SetSiren(true) }, KCmdSetAlarmFlashing, []byte{1, 1}},
		{"hflip", func(c *Client) error { return c.SetHorizontalFlip(true) }, KCmdSetHFlip, []byte{0, 0, 0, 0, 1, 0}},
		{"vflip", func(c *Client) error { return c.SetVerticalFlip(true) }, KCmdSetVFlip, []byte{0, 0, 0, 0, 0, 1}},
		{"take_photo", func(c *Client) error { return c.TakePhoto() }, KCmdTakePhoto, []byte{1}},
		{"rotate_action", func(c *Client) error { return c.RotateByAction(2, 5) }, KCmdRotateByAction, []byte{2, 2, 5}},
		{"rotate_degree", func(c *Client) error { return c.RotateByDegree(10, -5, 5) }, KCmdRotateByDegree, []byte{0x0a, 0x00, 0xfb, 0xff, 0x05}},
		{"reset_rotation", func(c *Client) error { return c.ResetRotation() }, KCmdResetRotate, []byte{3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &recordingConn{}
			c := &Client{conn: rc}
			require.NoError(t, tt.call(c))

			code, payload, ok := tutk.ParseHL(rc.lastWrite)
			require.True(t, ok, "output is not an HL frame")
			require.Equal(t, tt.code, code)
			require.Equal(t, tt.payload, payload)
		})
	}
}

// TestPTZPositionPayload checks the non-timestamp bytes (the timestamp prefix
// is time-dependent and not asserted).
func TestPTZPositionPayload(t *testing.T) {
	rc := &recordingConn{}
	c := &Client{conn: rc}
	require.NoError(t, c.SetPTZPosition(20, 180))

	code, payload, ok := tutk.ParseHL(rc.lastWrite)
	require.True(t, ok)
	require.Equal(t, uint16(KCmdSetPTZPosition), code)
	require.Len(t, payload, 7)
	require.Equal(t, byte(20), payload[4])
	require.Equal(t, uint16(180), binary.LittleEndian.Uint16(payload[5:]))
}

func TestControlValidation(t *testing.T) {
	c := &Client{conn: &recordingConn{}}
	require.Error(t, c.SetNightVision(9))
	require.Error(t, c.SetIRLED(0))
	require.Error(t, c.RotateByAction(9, 1))
	require.Error(t, c.RotateByDegree(0, 200, 1)) // vDeg outside int8
	require.Error(t, c.SetPTZPosition(99, 0))     // vertical > 40
	require.Error(t, c.SetPTZPosition(0, 999))    // horizontal > 350
}

func TestSendControlWaitSerialization(t *testing.T) {
	rc := &recordingConn{started: make(chan struct{}), release: make(chan struct{})}
	c := &Client{conn: rc}

	go func() { _, _ = c.SendControlWait(KCmdCheckCameraInfo, KCmdCheckCameraInfo+1, nil, time.Second) }()
	<-rc.started // first wait is now in flight

	_, err := c.SendControlWait(KCmdCheckCameraInfo, KCmdCheckCameraInfo+1, nil, time.Second)
	require.ErrorIs(t, err, ErrOpcodeSerialization)

	close(rc.release)
}

func TestSendControl_HLBytes(t *testing.T) {
	rc := &recordingConn{}
	c := &Client{conn: rc}
	require.NoError(t, c.SendControl(10042, []byte{3}))
	require.True(t, bytes.Equal(rc.lastWrite, tutk.HL(10042, []byte{3})))
}
