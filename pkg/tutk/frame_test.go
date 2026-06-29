package tutk

import (
	"encoding/binary"
	"testing"
)

// The packet builders below mirror the layouts decoded in ParsePacketHeader
// and extractPayload for the video channel.

// videoStart builds a 36-byte-header start packet (no FrameInfo).
func videoStart(frameNo uint32, pktTotal, pktIdx uint16, payload []byte) []byte {
	b := make([]byte, 36+len(payload))
	b[0] = ChannelIVideo
	b[1] = FrameTypeStart
	binary.LittleEndian.PutUint16(b[20:], pktTotal)
	binary.LittleEndian.PutUint16(b[22:], pktIdx)
	binary.LittleEndian.PutUint16(b[24:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(b[32:], frameNo)
	copy(b[36:], payload)
	return b
}

// videoCont builds a 28-byte-header continuation packet (no FrameInfo).
func videoCont(frameNo uint32, pktTotal, pktIdx uint16, payload []byte) []byte {
	b := make([]byte, 28+len(payload))
	b[0] = ChannelIVideo
	b[1] = FrameTypeCont
	binary.LittleEndian.PutUint16(b[12:], pktTotal)
	binary.LittleEndian.PutUint16(b[14:], pktIdx)
	binary.LittleEndian.PutUint16(b[16:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], frameNo)
	copy(b[28:], payload)
	return b
}

// videoEnd builds a 28-byte-header multi-packet end packet carrying the
// 40-byte FrameInfo. The 0x0028 marker makes ParsePacketHeader derive
// PktIdx as pktTotal-1 and flag HasFrameInfo.
func videoEnd(frameNo uint32, pktTotal uint16, payload []byte, codec byte, totalPayloadSize uint32) []byte {
	b := make([]byte, 28+len(payload)+frameInfoSize)
	b[0] = ChannelIVideo
	b[1] = FrameTypeEndMulti
	binary.LittleEndian.PutUint16(b[12:], pktTotal)
	binary.LittleEndian.PutUint16(b[14:], 0x0028) // marker -> HasFrameInfo
	binary.LittleEndian.PutUint16(b[16:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], frameNo)
	copy(b[28:], payload)

	fi := b[28+len(payload):]
	fi[0] = codec
	fi[2] = 0x01 // keyframe flag
	binary.LittleEndian.PutUint32(fi[16:], totalPayloadSize)
	binary.LittleEndian.PutUint32(fi[20:], frameNo)
	return b
}

// videoEndMarker builds a 28-byte-header end packet with a configurable
// FrameInfo marker (0x0028=40B standard, 0x0030=48B doorbell) and trailer size.
func videoEndMarker(frameNo uint32, pktTotal, marker uint16, payload []byte, codec byte, fiSize int) []byte {
	b := make([]byte, 28+len(payload)+fiSize)
	b[0] = ChannelIVideo
	b[1] = FrameTypeEndMulti
	binary.LittleEndian.PutUint16(b[12:], pktTotal)
	binary.LittleEndian.PutUint16(b[14:], marker)
	binary.LittleEndian.PutUint16(b[16:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], frameNo)
	copy(b[28:], payload)
	fi := b[28+len(payload):] // FrameInfo fields sit at the start of the trailer
	fi[0] = codec
	fi[2] = 0x01 // keyframe
	binary.LittleEndian.PutUint32(fi[20:], frameNo)
	return b
}

// drain non-blockingly collects everything currently queued for output.
func drain(h *FrameHandler) []*Packet {
	var out []*Packet
	for {
		select {
		case p := <-h.output:
			out = append(out, p)
		default:
			return out
		}
	}
}

func TestTsTracker(t *testing.T) {
	var tr tsTracker

	if got := tr.update(1000); got != 0 {
		t.Fatalf("first update = %d, want 0", got)
	}
	if got := tr.update(51000); got != 50000 {
		t.Errorf("after +50000us = %d, want 50000", got)
	}
	if got := tr.update(101000); got != 100000 {
		t.Errorf("after another +50000us = %d, want 100000", got)
	}

	// An implausibly large jump must be clamped (no accumulation).
	if got := tr.update(101000 + 10*tsWrapPeriod); got != 100000 {
		t.Errorf("after implausible jump = %d, want 100000 (clamped)", got)
	}
}

// TestHandleVideo_DoorbellTrailer verifies that a single-packet doorbell frame
// (48-byte FrameInfo) emits only the payload, with the larger trailer stripped.
func TestHandleVideo_DoorbellTrailer(t *testing.T) {
	h := NewFrameHandler(false)

	h.Handle(videoEndMarker(9, 1, 0x0030, []byte("PAYLOAD!"), CodecH264, 48))

	pkts := drain(h)
	if len(pkts) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(pkts))
	}
	if string(pkts[0].Payload) != "PAYLOAD!" {
		t.Errorf("Payload = %q, want %q (48-byte trailer not stripped?)", pkts[0].Payload, "PAYLOAD!")
	}
	if !pkts[0].IsKeyframe {
		t.Errorf("IsKeyframe = false, want true")
	}
}

func TestParsePacketHeader(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		wantNil      bool
		headerSize   int
		frameNo      uint32
		pktIdx       uint16
		pktTotal     uint16
		hasFrameInfo bool
	}{
		{
			name:       "start 36-byte header",
			data:       videoStart(7, 4, 0, []byte("AAAA")),
			headerSize: 36, frameNo: 7, pktIdx: 0, pktTotal: 4, hasFrameInfo: false,
		},
		{
			name:       "continuation 28-byte header",
			data:       videoCont(7, 4, 2, []byte("BBBB")),
			headerSize: 28, frameNo: 7, pktIdx: 2, pktTotal: 4, hasFrameInfo: false,
		},
		{
			name:       "multi-end carries FrameInfo, idx derived as total-1",
			data:       videoEnd(7, 4, []byte("CCCC"), CodecH264, 16),
			headerSize: 28, frameNo: 7, pktIdx: 3, pktTotal: 4, hasFrameInfo: true,
		},
		{
			name:    "too short returns nil",
			data:    make([]byte, 20),
			wantNil: true,
		},
		{
			// Doorbells (WYZEDB3) flag FrameInfo with a 0x0030 (48-byte) marker
			// instead of 0x0028 (40-byte). Regression guard for PR #2249.
			name:       "doorbell 48-byte FrameInfo marker",
			data:       videoEndMarker(9, 2, 0x0030, []byte("DDDD"), CodecH264, 48),
			headerSize: 28, frameNo: 9, pktIdx: 1, pktTotal: 2, hasFrameInfo: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := ParsePacketHeader(tt.data)
			if tt.wantNil {
				if hdr != nil {
					t.Fatalf("expected nil header, got %+v", hdr)
				}
				return
			}
			if hdr == nil {
				t.Fatal("expected header, got nil")
			}
			if hdr.HeaderSize != tt.headerSize {
				t.Errorf("HeaderSize = %d, want %d", hdr.HeaderSize, tt.headerSize)
			}
			if hdr.FrameNo != tt.frameNo {
				t.Errorf("FrameNo = %d, want %d", hdr.FrameNo, tt.frameNo)
			}
			if hdr.PktIdx != tt.pktIdx {
				t.Errorf("PktIdx = %d, want %d", hdr.PktIdx, tt.pktIdx)
			}
			if hdr.PktTotal != tt.pktTotal {
				t.Errorf("PktTotal = %d, want %d", hdr.PktTotal, tt.pktTotal)
			}
			if hdr.HasFrameInfo != tt.hasFrameInfo {
				t.Errorf("HasFrameInfo = %v, want %v", hdr.HasFrameInfo, tt.hasFrameInfo)
			}
		})
	}
}

func TestHandleVideo_CleanFrame(t *testing.T) {
	h := NewFrameHandler(false)

	h.Handle(videoStart(1, 3, 0, []byte("AAAA")))
	h.Handle(videoCont(1, 3, 1, []byte("BBBB")))
	h.Handle(videoEnd(1, 3, []byte("CCCC"), CodecH264, 12))

	pkts := drain(h)
	if len(pkts) != 1 {
		t.Fatalf("expected 1 emitted packet, got %d", len(pkts))
	}
	if pkts[0].FrameNo != 1 {
		t.Errorf("FrameNo = %d, want 1", pkts[0].FrameNo)
	}
	if string(pkts[0].Payload) != "AAAABBBBCCCC" {
		t.Errorf("Payload = %q, want %q", pkts[0].Payload, "AAAABBBBCCCC")
	}
	if !pkts[0].IsKeyframe {
		t.Errorf("IsKeyframe = false, want true")
	}
	if h.OOODrops() != 0 {
		t.Errorf("OOODrops = %d, want 0", h.OOODrops())
	}
}

// TestHandleVideo_Reordered verifies that a frame whose packets arrive out of
// order (including the FrameInfo-carrying end packet first) is still assembled
// correctly with no drops. This is the reorder-tolerance behavior that replaces
// the old drop-on-OOO logic and fixes the HLS breakage from #2215.
func TestHandleVideo_Reordered(t *testing.T) {
	h := NewFrameHandler(false)

	// Frame #1, packets delivered in reverse: end (idx 2, +FrameInfo), then
	// the two earlier packets.
	h.Handle(videoEnd(1, 3, []byte("CCCC"), CodecH264, 12))
	h.Handle(videoCont(1, 3, 1, []byte("BBBB")))
	h.Handle(videoStart(1, 3, 0, []byte("AAAA")))

	pkts := drain(h)
	if len(pkts) != 1 {
		t.Fatalf("reordered frame: expected 1 emitted packet, got %d", len(pkts))
	}
	if string(pkts[0].Payload) != "AAAABBBBCCCC" {
		t.Errorf("Payload = %q, want %q", pkts[0].Payload, "AAAABBBBCCCC")
	}
	if got := h.OOODrops(); got != 0 {
		t.Errorf("OOODrops = %d, want 0 (reorder is not a drop)", got)
	}
}

// TestHandleVideo_LostPacketDropAndRecover is the regression guard for #2215: a
// genuinely lost packet must drop the frame exactly once (not once per
// remaining packet), stragglers of that frame must be ignored, and the next
// frame must still assemble cleanly.
func TestHandleVideo_LostPacketDropAndRecover(t *testing.T) {
	h := NewFrameHandler(false)

	// Frame #1 expects packets 0,1,2 but packet 1 never arrives.
	h.Handle(videoStart(1, 3, 0, []byte("AAAA")))
	h.Handle(videoEnd(1, 3, []byte("CCCC"), CodecH264, 12)) // idx 2 +FrameInfo

	if got := h.OOODrops(); got != 0 {
		t.Fatalf("incomplete frame still in progress: OOODrops = %d, want 0", got)
	}

	// Frame #2 begins -> frame #1 is abandoned (one drop).
	h.Handle(videoStart(2, 2, 0, []byte("EEEE")))
	if got := h.OOODrops(); got != 1 {
		t.Fatalf("after newer frame began: OOODrops = %d, want 1", got)
	}

	// A late packet of the dropped frame #1 must be ignored, not re-dropped.
	h.Handle(videoCont(1, 3, 1, []byte("BBBB")))
	if got := h.OOODrops(); got != 1 {
		t.Fatalf("after straggler: OOODrops = %d, want 1 (no re-drop)", got)
	}

	// Frame #2 completes and assembles normally.
	h.Handle(videoEnd(2, 2, []byte("FFFF"), CodecH264, 8))

	pkts := drain(h)
	if len(pkts) != 1 {
		t.Fatalf("expected 1 recovered packet, got %d", len(pkts))
	}
	if pkts[0].FrameNo != 2 {
		t.Errorf("recovered FrameNo = %d, want 2", pkts[0].FrameNo)
	}
	if string(pkts[0].Payload) != "EEEEFFFF" {
		t.Errorf("recovered Payload = %q, want %q", pkts[0].Payload, "EEEEFFFF")
	}
	if got := h.OOODrops(); got != 1 {
		t.Errorf("final OOODrops = %d, want 1", got)
	}
}
