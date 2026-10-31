package wyze

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/stretchr/testify/require"
)

// fakeProducer satisfies core.Producer (via the embedded nil interface, whose
// methods are never called) and the control interface (explicit methods).
type fakeProducer struct {
	core.Producer
	spotlight *bool
	photo     bool
	rotDir    byte
	rotSpeed  byte
	ptzV      byte
	ptzH      uint16
}

func (f *fakeProducer) SetSpotlight(on bool) error              { f.spotlight = &on; return nil }
func (f *fakeProducer) SetIRLED(byte) error                     { return nil }
func (f *fakeProducer) SetNightVision(byte) error               { return nil }
func (f *fakeProducer) SetSiren(bool) error                     { return nil }
func (f *fakeProducer) SetHorizontalFlip(bool) error            { return nil }
func (f *fakeProducer) SetVerticalFlip(bool) error              { return nil }
func (f *fakeProducer) TakePhoto() error                        { f.photo = true; return nil }
func (f *fakeProducer) RotateByAction(d, s byte) error          { f.rotDir, f.rotSpeed = d, s; return nil }
func (f *fakeProducer) RotateByDegree(int16, int16, byte) error { return nil }
func (f *fakeProducer) SetPTZPosition(v byte, h uint16) error   { f.ptzV, f.ptzH = v, h; return nil }
func (f *fakeProducer) ResetRotation() error                    { return nil }
func (f *fakeProducer) GetPTZPosition() (byte, uint16, error)   { return 12, 345, nil }

func seedStream(t *testing.T, name string, prod core.Producer) {
	s, err := streams.New(name)
	require.NoError(t, err)
	s.AddProducer(prod)
	t.Cleanup(func() { streams.Delete(name) })
}

func TestHandlePOSTSpotlight(t *testing.T) {
	fake := &fakeProducer{}
	seedStream(t, "cam1", fake)

	req := httptest.NewRequest("POST", "/api/wyze/spotlight?src=cam1", strings.NewReader(`{"on":true}`))
	w := httptest.NewRecorder()
	handlePOSTSpotlight(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, fake.spotlight)
	require.True(t, *fake.spotlight)
}

func TestHandlePOSTTakePhoto(t *testing.T) {
	fake := &fakeProducer{}
	seedStream(t, "cam2", fake)

	req := httptest.NewRequest("POST", "/api/wyze/take_photo?src=cam2", nil)
	w := httptest.NewRecorder()
	handlePOSTTakePhoto(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, fake.photo)
}

func TestHandlePOSTSpotlight_CameraOffline(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/wyze/spotlight?src=nope", strings.NewReader(`{"on":true}`))
	w := httptest.NewRecorder()
	handlePOSTSpotlight(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	// Body must mention src/camera so the bridge maps it to "camera offline".
	body := w.Body.String()
	require.True(t, strings.Contains(body, "src") || strings.Contains(body, "camera"), "body: %q", body)
}

func TestHandlePOSTSpotlight_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/wyze/spotlight?src=cam1", nil)
	w := httptest.NewRecorder()
	handlePOSTSpotlight(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandlePOSTSpotlight_ReadOnly(t *testing.T) {
	prev := api.ReadOnly
	t.Cleanup(func() { api.ReadOnly = prev })
	api.ReadOnly = true

	req := httptest.NewRequest("POST", "/api/wyze/spotlight?src=cam1", strings.NewReader(`{"on":true}`))
	w := httptest.NewRecorder()
	handlePOSTSpotlight(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandlePOSTRotateAction_BodyKeys(t *testing.T) {
	for _, body := range []string{`{"direction":3,"speed":4}`, `{"dir":3,"speed":4}`} {
		fake := &fakeProducer{}
		seedStream(t, "camrot", fake)
		req := httptest.NewRequest("POST", "/api/wyze/rotate_action?src=camrot", strings.NewReader(body))
		w := httptest.NewRecorder()
		handlePOSTRotateAction(w, req)
		require.Equal(t, http.StatusOK, w.Code, body)
		require.Equal(t, byte(3), fake.rotDir, body)
		require.Equal(t, byte(4), fake.rotSpeed, body)
		streams.Delete("camrot")
	}
}

func TestHandlePOSTPTZPosition_BodyKeys(t *testing.T) {
	for _, body := range []string{`{"v":20,"h":180}`, `{"vertical":20,"horizontal":180}`} {
		fake := &fakeProducer{}
		seedStream(t, "camptz", fake)
		req := httptest.NewRequest("POST", "/api/wyze/ptz_position?src=camptz", strings.NewReader(body))
		w := httptest.NewRecorder()
		handlePOSTPTZPosition(w, req)
		require.Equal(t, http.StatusOK, w.Code, body)
		require.Equal(t, byte(20), fake.ptzV, body)
		require.Equal(t, uint16(180), fake.ptzH, body)
		streams.Delete("camptz")
	}
}

func TestHandleGETPTZState(t *testing.T) {
	seedStream(t, "camstate", &fakeProducer{})
	req := httptest.NewRequest("GET", "/api/wyze/ptz_state?src=camstate", nil)
	w := httptest.NewRecorder()
	handleGETPTZState(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"vertical":12,"horizontal":345}`, w.Body.String())
}
