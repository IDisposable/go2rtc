package wyze

import (
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
	"github.com/stretchr/testify/require"
)

func TestPCMFrameBytes(t *testing.T) {
	require.Equal(t, 320, pcmFrameBytes(tutk.CodecPCML, 160, 1))
	require.Equal(t, 160, pcmFrameBytes(tutk.CodecPCMU, 160, 1))
	require.Equal(t, 0, pcmFrameBytes(tutk.CodecAACADTS, 1024, 1))
}

func TestSplitFrames(t *testing.T) {
	got := splitFrames(make([]byte, 640), 320)
	require.Len(t, got, 2)
	require.Len(t, got[0], 320)
	got = splitFrames(make([]byte, 700), 320)
	require.Len(t, got, 3)
	require.Len(t, got[2], 60)
	require.Len(t, splitFrames(make([]byte, 100), 0), 1)
}

func TestBackchannelRate(t *testing.T) {
	require.Equal(t, uint32(16000), backchannelRate(tutk.CodecPCML, 8000))
	require.Equal(t, uint32(16000), backchannelRate(tutk.CodecPCML, 16000))
	require.Equal(t, uint32(8000), backchannelRate(tutk.CodecPCMU, 8000))
	require.Equal(t, uint32(16000), backchannelRate(tutk.CodecAACADTS, 16000))
}
