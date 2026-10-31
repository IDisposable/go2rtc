package wyze

import (
	"fmt"

	"github.com/AlexxIT/go2rtc/pkg/aac"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/tutk"
	"github.com/pion/rtp"
)

func (p *Producer) AddTrack(media *core.Media, codec *core.Codec, track *core.Receiver) error {
	if err := p.client.StartIntercom(); err != nil {
		return fmt.Errorf("wyze: failed to enable intercom: %w", err)
	}

	// Get the camera's audio codec info (what it sent us = what it accepts)
	tutkCodec, sampleRate, channels := p.client.GetBackchannelCodec()
	if tutkCodec == 0 {
		return fmt.Errorf("wyze: no audio codec detected from camera")
	}

	if p.client.verbose {
		fmt.Printf("[Wyze] Intercom enabled, using codec=0x%04x rate=%d ch=%d\n", tutkCodec, sampleRate, channels)
	}

	sender := core.NewSender(media, track.Codec)

	// Track our own timestamp - camera expects timestamps starting from 0
	// and incrementing by frame duration in microseconds
	var timestamp uint32 = 0
	samplesPerFrame := tutk.GetSamplesPerFrame(tutkCodec)
	frameDurationUS := samplesPerFrame * 1000000 / sampleRate

	// The camera times playback by the frame timestamp and assumes
	// samplesPerFrame per frame. RTP packets from ffmpeg often carry more
	// (e.g. 320 PCM samples = 40ms), which played back at 2x speed. Split
	// raw PCM payloads into exact samplesPerFrame chunks so every frame's
	// duration matches the timestamp step.
	frameBytes := pcmFrameBytes(tutkCodec, samplesPerFrame, channels)

	sender.Handler = func(pkt *rtp.Packet) {
		for _, chunk := range splitFrames(pkt.Payload, frameBytes) {
			if err := p.client.WriteAudio(tutkCodec, chunk, timestamp, sampleRate, channels); err == nil {
				p.Send += len(chunk)
			}
			timestamp += frameDurationUS
		}
	}

	switch track.Codec.Name {
	case core.CodecAAC:
		if track.Codec.IsRTP() {
			sender.Handler = aac.RTPToADTS(codec, sender.Handler)
		} else {
			sender.Handler = aac.EncodeToADTS(codec, sender.Handler)
		}
	}

	sender.HandleRTP(track)
	p.Senders = append(p.Senders, sender)

	return nil
}

// pcmFrameBytes returns the byte size of one camera audio frame for raw PCM
// codecs, or 0 for compressed codecs (sent as-is, one packet per frame).
func pcmFrameBytes(codec byte, samplesPerFrame uint32, channels uint8) int {
	if channels == 0 {
		channels = 1
	}
	switch codec {
	case tutk.CodecPCML:
		return int(samplesPerFrame) * 2 * int(channels)
	case tutk.CodecPCMU, tutk.CodecPCMA:
		return int(samplesPerFrame) * int(channels)
	}
	return 0
}

// splitFrames cuts payload into frameBytes-sized chunks; the final short
// chunk is kept. frameBytes <= 0 returns the payload unchanged.
func splitFrames(payload []byte, frameBytes int) [][]byte {
	if frameBytes <= 0 || len(payload) <= frameBytes {
		return [][]byte{payload}
	}
	out := make([][]byte, 0, (len(payload)+frameBytes-1)/frameBytes)
	for len(payload) > frameBytes {
		out = append(out, payload[:frameBytes])
		payload = payload[frameBytes:]
	}
	return append(out, payload)
}
