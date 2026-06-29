package dtls

import (
	"encoding/binary"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
	"github.com/stretchr/testify/require"
)

// TestMsgAudioFrame locks the on-wire layout of the two-way-audio (intercom)
// frame the backchannel sends to the camera. The byte offsets are magic
// (capture-derived), so a golden test guards against drift.
func TestMsgAudioFrame(t *testing.T) {
	c := &DTLSConn{}
	payload := []byte("\x01\x02\x03\x04")

	frame := c.msgAudioFrame(payload, 0, tutk.CodecPCMU, 8000, 1)

	require.Len(t, frame, 36+len(payload)+16)

	// Outer header.
	require.Equal(t, tutk.ChannelAudio, frame[0])
	require.Equal(t, tutk.FrameTypeStartAlt, frame[1])
	require.Equal(t, uint16(0x000c), binary.LittleEndian.Uint16(frame[2:]))
	require.Equal(t, uint32(1), binary.LittleEndian.Uint32(frame[4:]), "audioSeq starts at 1")
	require.Equal(t, uint32(0x00000001), binary.LittleEndian.Uint32(frame[12:]), "first-frame marker")

	// Inner header.
	require.Equal(t, tutk.ChannelAudio, frame[16])
	require.Equal(t, tutk.FrameTypeEndSingle, frame[17])
	require.Equal(t, uint16(0x0001), binary.LittleEndian.Uint16(frame[20:]), "pkt_total")
	require.Equal(t, uint32(len(payload)+16), binary.LittleEndian.Uint32(frame[24:]))
	require.Equal(t, uint32(1), binary.LittleEndian.Uint32(frame[32:]), "audioFrameNo")

	// Payload then 16-byte FrameInfo trailer.
	require.Equal(t, payload, frame[36:36+len(payload)])
	fi := frame[36+len(payload):]
	require.Equal(t, tutk.CodecPCMU, fi[0])
	// flags: [3:2]=sampleRateIdx(8000->0), bit1 16-bit set, bit0 stereo unset.
	require.Equal(t, byte(0x02), fi[2])

	// Second frame advances the sequence/frame counters and flips the marker.
	frame2 := c.msgAudioFrame(payload, 0, tutk.CodecPCMU, 8000, 1)
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(frame2[4:]))
	require.Equal(t, uint32(0x00100001), binary.LittleEndian.Uint32(frame2[12:]), "non-first marker")
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(frame2[32:]))
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(frame2[18:]), "prevFrame")
}

func TestDerivePSK(t *testing.T) {
	// PSK is SHA256(enr) truncated at the first NUL byte (C-string semantics).
	psk := DerivePSK("test-enr-value")
	require.Len(t, psk, 32)

	// Same input is deterministic.
	require.Equal(t, psk, DerivePSK("test-enr-value"))
}

func TestCalculateAuthKey(t *testing.T) {
	// Auth key is URL-safe-ish base64 of SHA256(enr+upper(mac))[:6] with
	// +/= remapped. MAC case must not matter.
	k1 := CalculateAuthKey("enr123", "aabbccddeeff")
	k2 := CalculateAuthKey("enr123", "AABBCCDDEEFF")
	require.Equal(t, k1, k2)
	require.Len(t, k1, 8) // base64 of 6 bytes
	require.NotContains(t, string(k1), "+")
	require.NotContains(t, string(k1), "/")
	require.NotContains(t, string(k1), "=")
}
