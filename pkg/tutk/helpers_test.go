package tutk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHLRoundTrip checks the Wyze "HL" control-message framing used by every
// K-command (auth, control channel, resolution).
func TestHLRoundTrip(t *testing.T) {
	payload := []byte(`{"cameraInfo":{}}`)
	msg := HL(10000, payload)

	require.Equal(t, byte('H'), msg[0])
	require.Equal(t, byte('L'), msg[1])
	require.Equal(t, byte(5), msg[2]) // version

	cmdID, gotPayload, ok := ParseHL(msg)
	require.True(t, ok)
	require.Equal(t, uint16(10000), cmdID)
	require.Equal(t, payload, gotPayload)
}

func TestParseHL_Invalid(t *testing.T) {
	_, _, ok := ParseHL([]byte("XX"))
	require.False(t, ok)

	_, _, ok = ParseHL(nil)
	require.False(t, ok)
}

// TestFindHL locates an HL message embedded after an outer header, which is how
// responses arrive inside IOCtrl/channel envelopes.
func TestFindHL(t *testing.T) {
	inner := HL(10001, []byte("body"))
	wrapped := append(make([]byte, 32), inner...)

	found := FindHL(wrapped, 16)
	require.NotNil(t, found)

	cmdID, payload, ok := ParseHL(found)
	require.True(t, ok)
	require.Equal(t, uint16(10001), cmdID)
	require.Equal(t, []byte("body"), payload)

	require.Nil(t, FindHL([]byte("no header here"), 0))
}
