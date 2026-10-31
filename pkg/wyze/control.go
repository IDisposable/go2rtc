package wyze

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
)

// Wyze HL K-command opcodes. Cross-referenced against
// wyzecam/tutk_protocol.py @ mrlt8/docker-wyze-bridge (HW-tested catalog) and
// AlexxIT/go2rtc pkg/tutk/helpers.go. Response opcode is always Kxxxxx+1; only
// one outstanding request per opcode is safe (correlation is by code).
const (
	KCmdCheckCameraInfo   = 10020
	KCmdCheckCameraParams = 10020 // same opcode, different payload
	KCmdCheckConnStatus   = 10446
	KCmdGetBatteryUsage   = 10448

	// Status light (network LED)
	KCmdGetStatusLight = 10030
	KCmdSetStatusLight = 10032 // [value:1] 1=on 2=off

	// Night vision
	KCmdGetNightVision = 10040
	KCmdSetNightVision = 10042 // [status:1] 1=on 2=off 3=auto

	// IR LED
	KCmdGetIRLED = 10044
	KCmdSetIRLED = 10046 // [status:1] 1=850nm long-range 2=940nm short-range

	// Video params (K10052 is multiplexed — subclasses share the opcode)
	KCmdGetVideoParam   = 10050
	KCmdSetVideoParamDB = 10052 // WYZEDB3/WVOD1/HL_WCO2/WYZEC1
	KCmdSetFPS          = 10052 // [0,0,0,fps,0,0]
	KCmdSetBitrate      = 10052 // <HBBBB bitrate + 4 zeros
	KCmdSetHFlip        = 10052 // [0,0,0,0,h,0]
	KCmdSetVFlip        = 10052 // [0,0,0,0,0,v]

	// OSD
	KCmdGetOSDStatus     = 10070
	KCmdSetOSDStatus     = 10072 // [value:1]
	KCmdGetOSDLogoStatus = 10074
	KCmdSetOSDLogoStatus = 10076 // [value:1]

	// Time + timezone
	KCmdGetCameraTime = 10090
	KCmdSetCameraTime = 10092 // <I timestamp
	KCmdSetTimeZone   = 10302 // <b signed int8 -11..13

	// Motion (SET opcode differs by model: old models K10202, others K10206)
	KCmdGetMotionAlarm    = 10200
	KCmdSetMotionAlarmOld = 10202 // WYZEDB3/WVOD1/HL_WCO2/WYZEC1: [value:1, 0]
	KCmdSetMotionAlarmNew = 10206 // everything else: [value:1, 0]
	KCmdGetMotionTagging  = 10290
	KCmdSetMotionTagging  = 10292 // [value:1]

	// Take photo (to SD card)
	KCmdTakePhoto = 10058 // [1]

	// Night mode auto-switch threshold (dusk vs dark)
	KCmdCheckNight             = 10620
	KCmdGetAutoSwitchNightType = 10624
	KCmdSetAutoSwitchNightType = 10626 // [type:1] 1=Dusk 2=Dark

	// Alarm / siren — fires siren + flashing LED together
	KCmdSetAlarmFlashing = 10630 // [v:1, v:1] value duplicated
	KCmdGetAlarmFlashing = 10632

	// Spotlight (WYZEC3L / floodlight lineage)
	KCmdGetSpotlight = 10640
	KCmdSetSpotlight = 10646 // [status:1]

	// RTSP switch
	KCmdSetRtspSwitch = 10600 // [value:1]
	KCmdGetRtspParam  = 10604

	// Pan/tilt motor
	KCmdRotateByDegree = 11000 // <hhB h:i16 v:i16 speed:u8
	KCmdRotateByAction = 11002 // [dir:1, dir:1, speed:1] (dir duplicated)
	KCmdResetRotate    = 11004 // [position:1]
	KCmdGetCurCruise   = 11006 // <I ts; resp <IBH ts + v:u8 + h:u16
	KCmdSetPTZPosition = 11018 // <IBH ts_ms + v:u8(0-40) + h:u16(0-350)
	KCmdGetMotionTrack = 11020
	KCmdSetMotionTrack = 11022 // [value:1]

	// Doorbell quick-response canned phrases (not siren!)
	KCmdResponseQuickMessage = 11635 // [value:1] 1-3

	// Destructive — deliberately not surfaced via typed wrappers.
	KCmdFormatSDCard   = 10242
	KCmdSetDeviceState = 10444
)

// ErrOpcodeSerialization is returned when SendControlWait is called for an
// opcode that already has an outstanding wait. Wyze correlates responses purely
// by opcode, so only one outstanding request per opcode is safe.
var ErrOpcodeSerialization = errors.New("wyze: opcode already has outstanding wait")

// SendControl frames an HL K-command and sends it fire-and-forget.
func (c *Client) SendControl(cmdID uint16, payload []byte) error {
	return c.conn.WriteIOCtrl(tutk.HL(cmdID, payload))
}

// SendControlWait sends an HL K-command and waits for the respID response.
// Only one wait per opcode may be outstanding (ErrOpcodeSerialization).
func (c *Client) SendControlWait(cmdID, respID uint16, payload []byte, timeout time.Duration) ([]byte, error) {
	c.ctrlMu.Lock()
	if c.ctrlWaits == nil {
		c.ctrlWaits = make(map[uint16]struct{})
	}
	if _, busy := c.ctrlWaits[cmdID]; busy {
		c.ctrlMu.Unlock()
		return nil, ErrOpcodeSerialization
	}
	c.ctrlWaits[cmdID] = struct{}{}
	c.ctrlMu.Unlock()

	defer func() {
		c.ctrlMu.Lock()
		delete(c.ctrlWaits, cmdID)
		c.ctrlMu.Unlock()
	}()

	return c.conn.WriteAndWaitIOCtrl(tutk.HL(cmdID, payload), c.matchHL(respID), timeout)
}

// ─── Typed wrappers on Client ──────────────────────────────────────

// SetNightVision picks status: 1=on, 2=off, 3=auto.
func (c *Client) SetNightVision(mode byte) error {
	if mode < 1 || mode > 3 {
		return fmt.Errorf("wyze: night_vision mode %d outside [1,3]", mode)
	}
	return c.SendControl(KCmdSetNightVision, []byte{mode})
}

// SetIRLED picks illuminator: 1=850nm long-range, 2=940nm short-range.
func (c *Client) SetIRLED(mode byte) error {
	if mode < 1 || mode > 2 {
		return fmt.Errorf("wyze: ir_led mode %d outside [1,2]", mode)
	}
	return c.SendControl(KCmdSetIRLED, []byte{mode})
}

// SetSpotlight toggles the spotlight. On=1, Off=2 on the wire.
func (c *Client) SetSpotlight(on bool) error {
	return c.SendControl(KCmdSetSpotlight, []byte{onOff(on)})
}

// SetSiren fires the alarm siren + flashing LED. Value is duplicated in the
// 2-byte payload (Wyze protocol quirk).
func (c *Client) SetSiren(on bool) error {
	v := onOff(on)
	return c.SendControl(KCmdSetAlarmFlashing, []byte{v, v})
}

// SetHorizontalFlip flips the image horizontally (K10052, h at index [4]).
func (c *Client) SetHorizontalFlip(on bool) error {
	return c.SendControl(KCmdSetHFlip, []byte{0, 0, 0, 0, onOff(on), 0})
}

// SetVerticalFlip flips the image vertically (K10052, v at index [5]).
func (c *Client) SetVerticalFlip(on bool) error {
	return c.SendControl(KCmdSetVFlip, []byte{0, 0, 0, 0, 0, onOff(on)})
}

// TakePhoto asks the camera to save a photo to its SD card.
func (c *Client) TakePhoto() error {
	return c.SendControl(KCmdTakePhoto, []byte{1})
}

// RotateByAction nudges the pan/tilt motor one step in a direction.
// dir: 0=stop, 1=left, 2=right, 3=up, 4=down. speed: 1-9 (0 = default 5).
//
// K11002 payload is [horizontal, vertical, speed] where horizontal is
// 1=left/2=right and vertical is 1=up/2=down (wyzecam K11002SetRotaryByAction).
// The axes are separate bytes; sending dir in both would move diagonally.
func (c *Client) RotateByAction(dir, speed byte) error {
	var h, v byte
	switch dir {
	case 0:
		// stop: both axes idle
	case 1, 2:
		h = dir
	case 3, 4:
		v = dir - 2
	default:
		return fmt.Errorf("wyze: rotate direction %d outside [0,4]", dir)
	}
	return c.SendControl(KCmdRotateByAction, []byte{h, v, clampSpeed(speed)})
}

// clampSpeed maps a requested motor speed onto the firmware's 1-9 range,
// using the wyzecam default of 5 when unset or out of range.
func clampSpeed(speed byte) byte {
	if speed < 1 || speed > 9 {
		return 5
	}
	return speed
}

// RotateByDegree moves by relative degrees. Vertical is int16 on the wire but
// firmware rejects values outside int8 range; enforce client-side to fail fast.
func (c *Client) RotateByDegree(hDeg, vDeg int16, speed byte) error {
	if vDeg < -128 || vDeg > 127 {
		return fmt.Errorf("wyze: rotate vDeg %d outside firmware limit int8", vDeg)
	}
	p := make([]byte, 5)
	binary.LittleEndian.PutUint16(p[0:], uint16(hDeg))
	binary.LittleEndian.PutUint16(p[2:], uint16(vDeg))
	p[4] = clampSpeed(speed)
	return c.SendControl(KCmdRotateByDegree, p)
}

// SetPTZPosition moves to an absolute position. Timestamp is injected here
// (camera uses it for sequencing). Ranges are model-dependent: Pan v1/v2 tilt
// 0-40, Pan v3 reports tilt 0-180 (0 = straight up) and pan 0-359, so only the
// widest physical bounds are enforced here; the firmware clamps the rest.
func (c *Client) SetPTZPosition(vertical byte, horizontal uint16) error {
	if vertical > 180 {
		return fmt.Errorf("wyze: ptz vertical %d outside [0,180]", vertical)
	}
	if horizontal > 359 {
		return fmt.Errorf("wyze: ptz horizontal %d outside [0,359]", horizontal)
	}
	p := make([]byte, 7)
	binary.LittleEndian.PutUint32(p[0:], uint32(time.Now().UnixMilli()%1_000_000_000))
	p[4] = vertical
	binary.LittleEndian.PutUint16(p[5:], horizontal)
	return c.SendControl(KCmdSetPTZPosition, p)
}

// GetPTZPosition asks the motor for its current absolute position.
// K11006 request carries a uint32 timestamp; the K11007 response payload is
// <IBH = uint32 timestamp, uint8 vertical (0-40), uint16 horizontal (0-350).
func (c *Client) GetPTZPosition() (vertical byte, horizontal uint16, err error) {
	c.ptzReadMu.Lock()
	defer c.ptzReadMu.Unlock()
	req := make([]byte, 4)
	binary.LittleEndian.PutUint32(req, uint32(time.Now().Unix()))
	data, err := c.SendControlWait(KCmdGetCurCruise, KCmdGetCurCruise+1, req, 5*time.Second)
	if err != nil {
		return 0, 0, err
	}
	return parsePTZPosition(c.extractHL(data))
}

func parsePTZPosition(hl []byte) (vertical byte, horizontal uint16, err error) {
	_, payload, ok := tutk.ParseHL(hl)
	if !ok || len(payload) < 7 {
		return 0, 0, fmt.Errorf("wyze: short K11007 response (%d bytes)", len(payload))
	}
	return payload[4], binary.LittleEndian.Uint16(payload[5:]), nil
}

// ResetRotation returns the motor to its stored home position.
func (c *Client) ResetRotation() error {
	return c.SendControl(KCmdResetRotate, []byte{3})
}

func onOff(on bool) byte {
	if on {
		return 1
	}
	return 2
}

// ─── Producer forwards (drive the live stream's camera) ────────────

func (p *Producer) Control(cmdID uint16, payload []byte) error {
	return p.client.SendControl(cmdID, payload)
}
func (p *Producer) SetNightVision(mode byte) error       { return p.client.SetNightVision(mode) }
func (p *Producer) SetIRLED(mode byte) error             { return p.client.SetIRLED(mode) }
func (p *Producer) SetSpotlight(on bool) error           { return p.client.SetSpotlight(on) }
func (p *Producer) SetSiren(on bool) error               { return p.client.SetSiren(on) }
func (p *Producer) SetHorizontalFlip(on bool) error      { return p.client.SetHorizontalFlip(on) }
func (p *Producer) SetVerticalFlip(on bool) error        { return p.client.SetVerticalFlip(on) }
func (p *Producer) TakePhoto() error                     { return p.client.TakePhoto() }
func (p *Producer) RotateByAction(dir, speed byte) error { return p.client.RotateByAction(dir, speed) }
func (p *Producer) RotateByDegree(h, v int16, s byte) error {
	return p.client.RotateByDegree(h, v, s)
}
func (p *Producer) SetPTZPosition(v byte, h uint16) error { return p.client.SetPTZPosition(v, h) }
func (p *Producer) ResetRotation() error                  { return p.client.ResetRotation() }
func (p *Producer) GetPTZPosition() (byte, uint16, error) { return p.client.GetPTZPosition() }
