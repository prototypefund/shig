package mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"github.com/shigde/sfu/internal/rtp"
)

var (
	Answer                      = &webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: "--a--"}
	Offer                       = &webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "--o--"}
	OnQuitSessionInternallyStub = func(ctx context.Context, user uuid.UUID) bool {
		return true
	}
)

type RtpEngineMock struct {
	conn *rtp.Endpoint
	err  error
}

func NewRtpEngine() *RtpEngineMock {
	return &RtpEngineMock{}
}

func NewRtpEngineForOffer(answer *webrtc.SessionDescription) *RtpEngineMock {
	engine := NewRtpEngine()
	engine.conn = NewEndpoint(answer)
	return engine
}

func (e *RtpEngineMock) EstablishEndpoint(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ webrtc.SessionDescription, _ rtp.EndpointType, _ ...rtp.EndpointOption) (*rtp.Endpoint, error) {
	return e.conn, e.err
}
