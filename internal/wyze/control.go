package wyze

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/streams"
)

// controller is the subset of *wyze.Producer the control endpoints drive.
// Declared as an interface so handlers are testable with a fake.
type controller interface {
	SetSpotlight(on bool) error
	SetIRLED(mode byte) error
	SetNightVision(mode byte) error
	SetSiren(on bool) error
	SetHorizontalFlip(on bool) error
	SetVerticalFlip(on bool) error
	TakePhoto() error
	RotateByAction(dir, speed byte) error
	RotateByDegree(h, v int16, s byte) error
	SetPTZPosition(v byte, h uint16) error
	ResetRotation() error
	GetPTZPosition() (byte, uint16, error)
}

type controlSpec struct {
	name    string
	handler http.HandlerFunc
}

var controlEndpoints = []controlSpec{
	{"spotlight", handlePOSTSpotlight},
	{"ir_led", handlePOSTIRLED},
	{"night_vision", handlePOSTNightVision},
	{"siren", handlePOSTSiren},
	{"hor_flip", handlePOSTHFlip},
	{"ver_flip", handlePOSTVFlip},
	{"take_photo", handlePOSTTakePhoto},
	{"rotate_action", handlePOSTRotateAction},
	{"rotate_degree", handlePOSTRotateDegree},
	{"ptz_position", handlePOSTPTZPosition},
	{"reset_rotation", handlePOSTResetRotation},
	{"ptz_state", handleGETPTZState},
}

func registerControlEndpoints() {
	for _, spec := range controlEndpoints {
		api.HandleFunc("api/wyze/"+spec.name, spec.handler)
	}
}

// findWyzeController locates the live wyze camera producer for a stream. The
// error text mentions "camera" so the bridge can map it to "camera offline".
func findWyzeController(src string) (controller, error) {
	stream := streams.Get(src)
	if stream == nil {
		return nil, fmt.Errorf("wyze camera stream %q not found", src)
	}
	for _, p := range stream.Producers() {
		if c, ok := p.(controller); ok {
			return c, nil
		}
	}
	return nil, fmt.Errorf("stream %q has no active wyze camera", src)
}

func controlPreamble(w http.ResponseWriter, r *http.Request) (controller, bool) {
	if api.IsReadOnly() {
		api.ReadOnlyError(w)
		return nil, false
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return nil, false
	}
	ctrl, err := findWyzeController(r.URL.Query().Get("src"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return nil, false
	}
	return ctrl, true
}

// decodeBody reads the JSON request body into v. Empty bodies are allowed.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// runControl maps the camera command outcome onto HTTP status.
func runControl(w http.ResponseWriter, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handlePOSTSpotlight(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ On bool }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetSpotlight(body.On))
}

func handlePOSTIRLED(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ Mode byte }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetIRLED(body.Mode))
}

func handlePOSTNightVision(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ Mode byte }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetNightVision(body.Mode))
}

func handlePOSTSiren(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ On bool }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetSiren(body.On))
}

func handlePOSTHFlip(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ On bool }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetHorizontalFlip(body.On))
}

func handlePOSTVFlip(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct{ On bool }
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.SetVerticalFlip(body.On))
}

func handlePOSTTakePhoto(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	runControl(w, ctrl.TakePhoto())
}

func handlePOSTRotateAction(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	// "direction" is what the wyze-bridge client sends; "dir" kept as alias.
	var body struct {
		Direction byte `json:"direction"`
		Dir       byte `json:"dir"`
		Speed     byte `json:"speed"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	dir := body.Direction
	if dir == 0 {
		dir = body.Dir
	}
	runControl(w, ctrl.RotateByAction(dir, body.Speed))
}

func handlePOSTRotateDegree(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	var body struct {
		H, V  int16
		Speed byte
	}
	if !decodeBody(w, r, &body) {
		return
	}
	runControl(w, ctrl.RotateByDegree(body.H, body.V, body.Speed))
}

func handlePOSTPTZPosition(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	// Accept both the short "v"/"h" keys (wyze-bridge client) and the long form.
	var body struct {
		V          *byte   `json:"v"`
		H          *uint16 `json:"h"`
		Vertical   byte    `json:"vertical"`
		Horizontal uint16  `json:"horizontal"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.V != nil {
		body.Vertical = *body.V
	}
	if body.H != nil {
		body.Horizontal = *body.H
	}
	runControl(w, ctrl.SetPTZPosition(body.Vertical, body.Horizontal))
}

func handlePOSTResetRotation(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := controlPreamble(w, r)
	if !ok {
		return
	}
	runControl(w, ctrl.ResetRotation())
}

// handleGETPTZState reports the motor's current absolute position:
// {"vertical":0-40,"horizontal":0-350}. Read-only, so allowed in read-only mode.
func handleGETPTZState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	ctrl, err := findWyzeController(r.URL.Query().Get("src"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	v, h, err := ctrl.GetPTZPosition()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	api.ResponseJSON(w, map[string]any{"vertical": v, "horizontal": h})
}
