package tutk

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/AlexxIT/go2rtc/pkg/aac"
)

const (
	FrameTypeStart     uint8 = 0x08 // Extended start (36-byte header)
	FrameTypeStartAlt  uint8 = 0x09 // StartAlt (36-byte header)
	FrameTypeCont      uint8 = 0x00 // Continuation (28-byte header)
	FrameTypeContAlt   uint8 = 0x04 // Continuation alt
	FrameTypeEndSingle uint8 = 0x01 // Single-packet frame (28-byte)
	FrameTypeEndMulti  uint8 = 0x05 // Multi-packet end (28-byte)
	FrameTypeEndExt    uint8 = 0x0d // Extended end (36-byte)
)

const (
	ChannelIVideo uint8 = 0x05
	ChannelAudio  uint8 = 0x03
	ChannelPVideo uint8 = 0x07
)

const frameInfoSize = 40

// FrameInfo - Wyze extended FRAMEINFO (40 bytes at end of packet)
// Video: 40 bytes, Audio: 16 bytes (uses same struct, fields 16+ are zero)
//
// Offset  Size  Field
// 0-1     2     CodecID     - 0x4E=H264, 0x7B=H265, 0x90=AAC_WYZE
// 2       1     Flags       - Video: 1=Keyframe, 0=P-frame | Audio: sample rate/bits/channels
// 3       1     CamIndex    - Camera index
// 4       1     OnlineNum   - Online number
// 5       1     FPS         - Framerate (e.g. 20)
// 6       1     ResTier     - Video: 1=Low(360P), 4=High(HD/2K) | Audio: 0
// 7       1     Bitrate     - Video: 30=360P, 100=HD, 200=2K | Audio: 1
// 8-11    4     Timestamp   - Timestamp (increases ~50000/frame for 20fps video)
// 12-15   4     SessionID   - Session marker (constant per stream)
// 16-19   4     PayloadSize - Frame payload size in bytes
// 20-23   4     FrameNo     - Global frame number
// 24-35   12    DeviceID    - MAC address (ASCII) - video only
// 36-39   4     Padding     - Always 0 - video only
type FrameInfo struct {
	CodecID     byte   // 0 (only low byte used)
	Flags       uint8  // 2
	CamIndex    uint8  // 3
	OnlineNum   uint8  // 4
	FPS         uint8  // 5: Framerate
	ResTier     uint8  // 6: Resolution tier (1=Low, 4=High)
	Bitrate     uint8  // 7: Bitrate index (30=360P, 100=HD, 200=2K)
	Timestamp   uint32 // 8-11: Timestamp
	SessionID   uint32 // 12-15: Session marker (constant)
	PayloadSize uint32 // 16-19: Payload size
	FrameNo     uint32 // 20-23: Frame number
}

func (fi *FrameInfo) IsKeyframe() bool {
	return fi.Flags == 0x01
}

func (fi *FrameInfo) SampleRate() uint32 {
	idx := (fi.Flags >> 2) & 0x0F
	if idx < uint8(len(sampleRates)) {
		return sampleRates[idx]
	}
	return 16000
}

func (fi *FrameInfo) Channels() uint8 {
	if fi.Flags&0x01 == 1 {
		return 2
	}
	return 1
}

func ParseFrameInfo(data []byte) *FrameInfo {
	return ParseFrameInfoSize(data, frameInfoSize)
}

// ParseFrameInfoSize parses the FrameInfo from the last fiSize bytes of data.
// The struct fields live at the start of that trailer, so a larger doorbell
// trailer (48 bytes) is read from a deeper offset but the same field layout.
func ParseFrameInfoSize(data []byte, fiSize int) *FrameInfo {
	if fiSize < frameInfoSize {
		fiSize = frameInfoSize
	}
	if len(data) < fiSize {
		return nil
	}

	offset := len(data) - fiSize
	fi := data[offset:]

	return &FrameInfo{
		CodecID:     fi[0],
		Flags:       fi[2],
		CamIndex:    fi[3],
		OnlineNum:   fi[4],
		FPS:         fi[5],
		ResTier:     fi[6],
		Bitrate:     fi[7],
		Timestamp:   binary.LittleEndian.Uint32(fi[8:]),
		SessionID:   binary.LittleEndian.Uint32(fi[12:]),
		PayloadSize: binary.LittleEndian.Uint32(fi[16:]),
		FrameNo:     binary.LittleEndian.Uint32(fi[20:]),
	}
}

type Packet struct {
	Channel    uint8
	Codec      byte
	Timestamp  uint32
	Payload    []byte
	IsKeyframe bool
	FrameNo    uint32
	SampleRate uint32
	Channels   uint8
}

type PacketHeader struct {
	Channel       byte
	FrameType     byte
	HeaderSize    int
	FrameNo       uint32
	PktIdx        uint16
	PktTotal      uint16
	PayloadSize   uint16
	HasFrameInfo  bool
	FrameInfoSize int // trailer size from the marker (40=0x28 standard, 48=0x30 doorbell)
}

// isFrameInfoMarker reports whether the field that normally holds the packet
// index is instead a FrameInfo size marker. Standard devices use 0x0028 (40
// bytes); some doorbells use 0x0030 (48 bytes). A value >= PktTotal can't be a
// real packet index either, so it is treated as a marker too.
func isFrameInfoMarker(val, pktTotal uint16, frameType uint8) bool {
	if !IsEndFrame(frameType) && pktTotal != 1 {
		return false
	}
	return val == 0x0028 || (pktTotal > 0 && val >= pktTotal)
}

func ParsePacketHeader(data []byte) *PacketHeader {
	if len(data) < 28 {
		return nil
	}

	frameType := data[1]
	hdr := &PacketHeader{
		Channel:   data[0],
		FrameType: frameType,
	}

	switch frameType {
	case FrameTypeStart, FrameTypeStartAlt, FrameTypeEndExt:
		hdr.HeaderSize = 36
	default:
		hdr.HeaderSize = 28
	}

	if len(data) < hdr.HeaderSize {
		return nil
	}

	// 28-byte and 36-byte headers carry the same fields at different offsets.
	totalOff, idxOff, sizeOff, frameOff := 12, 14, 16, 24
	if hdr.HeaderSize == 36 {
		totalOff, idxOff, sizeOff, frameOff = 20, 22, 24, 32
	}

	hdr.PktTotal = binary.LittleEndian.Uint16(data[totalOff:])
	pktIdxOrMarker := binary.LittleEndian.Uint16(data[idxOff:])
	hdr.PayloadSize = binary.LittleEndian.Uint16(data[sizeOff:])
	hdr.FrameNo = binary.LittleEndian.Uint32(data[frameOff:])

	if isFrameInfoMarker(pktIdxOrMarker, hdr.PktTotal, frameType) {
		hdr.HasFrameInfo = true
		hdr.FrameInfoSize = int(pktIdxOrMarker) // marker value == trailer byte count
		if hdr.PktTotal > 0 {
			hdr.PktIdx = hdr.PktTotal - 1
		}
	} else {
		hdr.PktIdx = pktIdxOrMarker
	}

	return hdr
}

func IsStartFrame(frameType uint8) bool {
	return frameType == FrameTypeStart || frameType == FrameTypeStartAlt
}

func IsEndFrame(frameType uint8) bool {
	return frameType == FrameTypeEndSingle ||
		frameType == FrameTypeEndMulti ||
		frameType == FrameTypeEndExt
}

func IsContinuationFrame(frameType uint8) bool {
	return frameType == FrameTypeCont || frameType == FrameTypeContAlt
}

// maxFramePackets bounds the per-frame packet table so a corrupt header
// claiming a huge pktTotal can't trigger a giant allocation.
const maxFramePackets = 1024

// channelState reassembles one video frame at a time. Packets can arrive out
// of order over UDP, so each is placed into pkts by its index and the frame is
// emitted once every slot is filled and the FrameInfo (carried by the end
// packet) has been seen.
type channelState struct {
	frameNo   uint32     // frame currently being assembled (0 = none active)
	pktTotal  uint16     // expected packet count for the current frame
	pkts      [][]byte   // payloads indexed by PktIdx; nil = not yet received
	received  uint16     // number of filled slots in pkts
	frameInfo *FrameInfo // metadata from the packet that carries it
	lastDone  uint32     // last frameNo emitted or dropped (straggler guard)
	hasDone   bool       // lastDone has been set
}

// startFrame begins assembling frameNo, sizing the packet table to pktTotal.
// Returns false if pktTotal is implausible (corrupt header).
func (cs *channelState) startFrame(frameNo uint32, pktTotal uint16) bool {
	if pktTotal == 0 || pktTotal > maxFramePackets {
		return false
	}
	cs.frameNo = frameNo
	cs.pktTotal = pktTotal
	if cap(cs.pkts) >= int(pktTotal) {
		cs.pkts = cs.pkts[:pktTotal]
		for i := range cs.pkts {
			cs.pkts[i] = nil
		}
	} else {
		cs.pkts = make([][]byte, pktTotal)
	}
	cs.received = 0
	cs.frameInfo = nil
	return true
}

// done marks the current frame finished (emitted or dropped) so later
// stragglers for it are ignored, and clears the in-progress state.
func (cs *channelState) done() {
	cs.lastDone = cs.frameNo
	cs.hasDone = true
	cs.frameNo = 0
}

// frameNewer reports whether frame a is later than frame b, wrap-safe.
func frameNewer(a, b uint32) bool { return int32(a-b) > 0 }

const tsWrapPeriod uint32 = 1000000

// tsMaxPlausibleDeltaUS bounds a single inter-frame timestamp delta; anything
// larger contributes no time, so a corrupt or mis-modelled timestamp can't jump
// accumUS by minutes and shatter A/V sync.
//
// The update below assumes the camera timestamp is microseconds wrapping at
// tsWrapPeriod (every second); it may instead be a free-running uint32 µs
// counter wrapping at 2^32, unconfirmed against hardware. The clamp keeps both
// models safe.
const tsMaxPlausibleDeltaUS = 5 * tsWrapPeriod

type tsTracker struct {
	lastRawTS uint32
	accumUS   uint64
	firstTS   bool
}

func (t *tsTracker) update(rawTS uint32) uint64 {
	if !t.firstTS {
		t.firstTS = true
		t.lastRawTS = rawTS
		return 0
	}

	var delta uint32
	if rawTS >= t.lastRawTS {
		delta = rawTS - t.lastRawTS
	} else {
		// Wrapped: delta = (wrap - last) + new
		delta = (tsWrapPeriod - t.lastRawTS) + rawTS
	}

	if delta > tsMaxPlausibleDeltaUS {
		delta = 0 // implausible jump (glitch, reorder, or wrong wrap model)
	}

	t.accumUS += uint64(delta)
	t.lastRawTS = rawTS

	return t.accumUS
}

type FrameHandler struct {
	channels map[byte]*channelState
	videoTS  tsTracker
	audioTS  tsTracker
	output   chan *Packet
	verbose  bool
	closed   bool
	closeMu  sync.Mutex
	oooDrops uint64 // video frames dropped because a packet was lost (never arrived)
}

func (h *FrameHandler) OOODrops() uint64 {
	return h.oooDrops
}

func NewFrameHandler(verbose bool) *FrameHandler {
	return &FrameHandler{
		channels: make(map[byte]*channelState),
		output:   make(chan *Packet, 128),
		verbose:  verbose,
	}
}

func (h *FrameHandler) Recv() <-chan *Packet {
	return h.output
}

func (h *FrameHandler) Close() {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()

	if h.closed {
		return
	}
	h.closed = true
	close(h.output)
}

func (h *FrameHandler) Handle(data []byte) {
	hdr := ParsePacketHeader(data)
	if hdr == nil {
		return
	}

	payload, fi := h.extractPayload(data, hdr.Channel, hdr.FrameInfoSize)
	if payload == nil {
		return
	}

	if h.verbose {
		fiStr := ""
		if hdr.HasFrameInfo {
			fiStr = " +FI"
		}
		fmt.Printf("[RX] ch=0x%02x type=0x%02x #%d pkt=%d/%d data=%dB%s\n",
			hdr.Channel, hdr.FrameType,
			hdr.FrameNo, hdr.PktIdx, hdr.PktTotal, len(payload), fiStr)
	}

	switch hdr.Channel {
	case ChannelAudio:
		h.handleAudio(payload, fi)
	case ChannelIVideo, ChannelPVideo:
		h.handleVideo(hdr.Channel, hdr, payload, fi)
	}
}

func (h *FrameHandler) extractPayload(data []byte, channel byte, fiSizeHint int) ([]byte, *FrameInfo) {
	if len(data) < 2 {
		return nil, nil
	}

	frameType := data[1]
	fiSize := 0
	headerSize := 28

	switch frameType {
	case FrameTypeStart:
		headerSize = 36
	case FrameTypeStartAlt:
		headerSize = 36
		if len(data) >= 22 {
			pktTotal := binary.LittleEndian.Uint16(data[20:])
			if pktTotal == 1 {
				fiSize = frameInfoSize
			}
		}
	case FrameTypeEndSingle, FrameTypeEndMulti:
		fiSize = frameInfoSize
	case FrameTypeEndExt:
		headerSize = 36
		fiSize = frameInfoSize
	}

	// Honor the marker-derived trailer size (standard 40, doorbells 48).
	if fiSize > 0 && fiSizeHint > fiSize {
		fiSize = fiSizeHint
	}

	if len(data) < headerSize {
		return nil, nil
	}

	if fiSize == 0 {
		return data[headerSize:], nil
	}

	if len(data) < headerSize+fiSize {
		return data[headerSize:], nil
	}

	fi := ParseFrameInfoSize(data, fiSize)

	validCodec := false
	switch channel {
	case ChannelIVideo, ChannelPVideo:
		validCodec = IsVideoCodec(fi.CodecID)
	case ChannelAudio:
		validCodec = IsAudioCodec(fi.CodecID)
	}

	if validCodec {
		payload := data[headerSize : len(data)-fiSize]
		return payload, fi
	}

	return data[headerSize:], nil
}

func (h *FrameHandler) handleVideo(channel byte, hdr *PacketHeader, payload []byte, fi *FrameInfo) {
	cs := h.channels[channel]
	if cs == nil {
		cs = &channelState{}
		h.channels[channel] = cs
	}

	// Straggler from an already-finished frame.
	if cs.hasDone && !frameNewer(hdr.FrameNo, cs.lastDone) {
		return
	}

	if cs.frameNo == 0 || hdr.FrameNo != cs.frameNo {
		if cs.frameNo != 0 {
			if !frameNewer(hdr.FrameNo, cs.frameNo) {
				return // packet for an older, already-abandoned frame
			}
			// A newer frame began before the current one finished: the current
			// frame is missing packets that will never arrive. Drop it once.
			h.dropIncomplete(channel, cs)
		}
		if !cs.startFrame(hdr.FrameNo, hdr.PktTotal) {
			return
		}
	}

	if int(hdr.PktIdx) >= len(cs.pkts) {
		return // index past declared total - corrupt header
	}
	if cs.pkts[hdr.PktIdx] == nil {
		// payload aliases the shared read buffer, so copy it.
		cs.pkts[hdr.PktIdx] = append([]byte(nil), payload...)
		cs.received++
	}
	if fi != nil {
		cs.frameInfo = fi
	}

	// Need every packet plus the FrameInfo before emitting.
	if cs.received != cs.pktTotal || cs.frameInfo == nil {
		return
	}

	fi = cs.frameInfo
	frameNo := cs.frameNo

	var size int
	for _, p := range cs.pkts {
		size += len(p)
	}
	data := make([]byte, 0, size)
	for _, p := range cs.pkts {
		data = append(data, p...)
	}

	cs.done()

	if fi.PayloadSize > 0 && uint32(len(data)) != fi.PayloadSize {
		if h.verbose {
			fmt.Printf("[SIZE] ch=0x%02x #%d mismatch: expected %d, got %d\n",
				channel, frameNo, fi.PayloadSize, len(data))
		}
		return
	}

	if len(data) == 0 {
		return
	}

	prevRaw := h.videoTS.lastRawTS
	accumUS := h.videoTS.update(fi.Timestamp)
	rtpTS := uint32(accumUS * 90000 / 1000000)

	if h.verbose {
		fmt.Printf("[TS] ch=0x%02x raw=%d delta=%d accum_us=%d\n",
			channel, fi.Timestamp, fi.Timestamp-prevRaw, accumUS)
	}

	pkt := &Packet{
		Channel:    channel,
		Payload:    data,
		Codec:      fi.CodecID,
		Timestamp:  rtpTS,
		IsKeyframe: fi.IsKeyframe(),
		FrameNo:    fi.FrameNo,
	}

	if h.verbose {
		frameType := "P"
		if fi.IsKeyframe() {
			frameType = "KEY"
		}
		fmt.Printf("[OK] ch=0x%02x #%d codec=0x%02x %s size=%d\n",
			channel, fi.FrameNo, fi.CodecID, frameType, len(pkt.Payload))
		fmt.Printf("  [0-1]codec=0x%02x [2]flags=0x%x [3]=%d [4]=%d\n",
			fi.CodecID, fi.Flags, fi.CamIndex, fi.OnlineNum)
		fmt.Printf("  [5]=%d [6]=%d [7]=%d [8-11]ts=%d\n",
			fi.FPS, fi.ResTier, fi.Bitrate, fi.Timestamp)
		fmt.Printf("  [12-15]=0x%x [16-19]payload=%d [20-23]frameNo=%d\n",
			fi.SessionID, fi.PayloadSize, fi.FrameNo)
		fmt.Printf("  rtp_ts=%d accum_us=%d\n", rtpTS, accumUS)
		fmt.Printf("  hex: %s\n", dumpHex(fi))
	}

	h.queue(pkt)
}

// dropIncomplete records that the in-progress frame was abandoned because a
// later frame began before it finished (a packet was lost for good).
func (h *FrameHandler) dropIncomplete(channel byte, cs *channelState) {
	if h.verbose {
		fmt.Printf("[DROP] ch=0x%02x #%d incomplete: got %d/%d pkts\n",
			channel, cs.frameNo, cs.received, cs.pktTotal)
	}
	h.oooDrops++
	cs.done()
}

func (h *FrameHandler) handleAudio(payload []byte, fi *FrameInfo) {
	if len(payload) == 0 || fi == nil {
		return
	}

	var sampleRate uint32
	var channels uint8

	switch fi.CodecID {
	case CodecAACRaw, CodecAACADTS, CodecAACLATM, CodecAACAlt:
		sampleRate, channels = parseAudioParams(payload, fi)
	default:
		sampleRate = fi.SampleRate()
		channels = fi.Channels()
	}

	accumUS := h.audioTS.update(fi.Timestamp)
	rtpTS := uint32(accumUS * uint64(sampleRate) / 1000000)

	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	pkt := &Packet{
		Channel:    ChannelAudio,
		Payload:    payloadCopy,
		Codec:      fi.CodecID,
		Timestamp:  rtpTS,
		SampleRate: sampleRate,
		Channels:   channels,
		FrameNo:    fi.FrameNo,
	}

	if h.verbose {
		bits := 8
		if fi.Flags&0x02 != 0 {
			bits = 16
		}
		fmt.Printf("[OK] Audio #%d codec=0x%02x size=%d\n",
			fi.FrameNo, fi.CodecID, len(payload))
		fmt.Printf("  [0-1]codec=0x%02x [2]flags=0x%x(%dHz/%dbit/%dch)\n",
			fi.CodecID, fi.Flags, sampleRate, bits, channels)
		fmt.Printf("  [8-11]ts=%d [12-15]=0x%x rtp_ts=%d\n",
			fi.Timestamp, fi.SessionID, rtpTS)
		fmt.Printf("  hex: %s\n", dumpHex(fi))
	}

	h.queue(pkt)
}

func (h *FrameHandler) queue(pkt *Packet) {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()

	if h.closed {
		return
	}

	select {
	case h.output <- pkt:
	default:
		// Queue full - drop oldest
		select {
		case <-h.output:
		default:
		}
		select {
		case h.output <- pkt:
		default:
			// Queue still full, drop this packet
		}
	}
}

func parseAudioParams(payload []byte, fi *FrameInfo) (sampleRate uint32, channels uint8) {
	if aac.IsADTS(payload) {
		codec := aac.ADTSToCodec(payload)
		if codec != nil {
			return codec.ClockRate, codec.Channels
		}
	}

	if fi != nil {
		return fi.SampleRate(), fi.Channels()
	}

	return 16000, 1
}

func dumpHex(fi *FrameInfo) string {
	b := make([]byte, frameInfoSize)
	b[0] = fi.CodecID
	b[1] = 0 // High byte (unused)
	b[2] = fi.Flags
	b[3] = fi.CamIndex
	b[4] = fi.OnlineNum
	b[5] = fi.FPS
	b[6] = fi.ResTier
	b[7] = fi.Bitrate
	binary.LittleEndian.PutUint32(b[8:], fi.Timestamp)
	binary.LittleEndian.PutUint32(b[12:], fi.SessionID)
	binary.LittleEndian.PutUint32(b[16:], fi.PayloadSize)
	binary.LittleEndian.PutUint32(b[20:], fi.FrameNo)
	// Bytes 24-39 are DeviceID and Padding (not stored in struct)

	hexStr := hex.EncodeToString(b)
	formatted := ""
	for i := 0; i < len(hexStr); i += 2 {
		if i > 0 {
			formatted += " "
		}
		formatted += hexStr[i : i+2]
	}
	return formatted
}
