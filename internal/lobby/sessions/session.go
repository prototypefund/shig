package sessions

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"github.com/shigde/sfu/internal/rtp"
	"github.com/shigde/sfu/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/exp/slog"
)

const sessionTrasser = "github.com/shigde/sfu/internal/lobby/sessions"

type RtpEngine interface {
	EstablishIngressEndpoint(ctx context.Context, sessionId uuid.UUID, liveStream uuid.UUID, offer webrtc.SessionDescription, options ...rtp.EndpointOption) (*rtp.Endpoint, error)
	EstablishEgressEndpoint(ctx context.Context, sessionId uuid.UUID, liveStream uuid.UUID, options ...rtp.EndpointOption) (*rtp.Endpoint, error)
	EstablishStaticEgressEndpoint(ctx context.Context, sessionId uuid.UUID, liveStream uuid.UUID, offer webrtc.SessionDescription, options ...rtp.EndpointOption) (*rtp.Endpoint, error)
}

var (
	ErrIngressAlreadyExists = errors.New("ingress resource already exists in session")
	ErrEgressAlreadyExists  = errors.New("egress resource already exists in session")

	iceGatheringTimeout = 5 * time.Second
)

type Session struct {
	*sync.RWMutex
	Id       uuid.UUID
	ctx      context.Context
	user     uuid.UUID
	isRemote bool
	hub      *Hub

	rtpEngine     RtpEngine
	ingress       *rtp.Endpoint
	egress        *rtp.Endpoint
	signalChannel *rtp.Endpoint
	signal        *signal

	stop    context.CancelFunc
	garbage chan<- Item
}

func NewSession(ctx context.Context, user uuid.UUID, hub *Hub, engine RtpEngine, garbage chan Item) *Session {
	ctx, cancel := context.WithCancel(ctx)
	sessionId := uuid.New()
	signal := newSignal(ctx, sessionId, user)

	session := &Session{
		Id:   uuid.New(),
		ctx:  ctx,
		user: user,

		rtpEngine: engine,
		hub:       hub,
		signal:    signal,
		stop:      cancel,
		garbage:   garbage,
	}

	// signal.onMuteCbk = session.onMuteTrack
	//go session.run(ctx)

	return session
}

func (s *Session) CreateIngressEndpoint(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	ctx, span := s.trace(ctx, "create-ingress-endpoint")
	defer span.End()

	if s.ingress != nil {
		telemetry.RecordError(span, ErrIngressAlreadyExists)
		return nil, ErrIngressAlreadyExists
	}

	option := make([]rtp.EndpointOption, 0)
	option = append(option, rtp.EndpointWithDataChannel(s.signal.OnIngressChannel))
	option = append(option, rtp.EndpointWithLostConnectionListener(s.onLostConnection))
	option = append(option, rtp.EndpointWithTrackDispatcher(s.hub))

	endpoint, err := s.rtpEngine.EstablishIngressEndpoint(s.ctx, s.Id, s.hub.LiveStreamId, *offer, option...)
	if err != nil {
		return nil, telemetry.RecordErrorf(span, "create rtp connection", err)
	}
	s.ingress = endpoint

	span.AddEvent("Wait for Local Description.")
	ctxTimeout, cancel := context.WithTimeout(ctx, iceGatheringTimeout)
	defer cancel()
	answer, err := s.ingress.GetLocalDescription(ctxTimeout)
	if err != nil {
		return nil, telemetry.RecordErrorf(span, "create ingress resource endpoint", err)
	}

	span.AddEvent("Receive Local Description.")
	return answer, nil
}

func (s *Session) CreateEgressEndpoint(ctx context.Context, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	ctx, span := s.trace(ctx, "create-egress-endpoint")
	defer span.End()

	if s.ingress != nil {
		telemetry.RecordError(span, ErrEgressAlreadyExists)
		return nil, ErrEgressAlreadyExists
	}

	option := make([]rtp.EndpointOption, 0)
	option = append(option, rtp.EndpointWithDataChannel(s.signal.OnIngressChannel))
	option = append(option, rtp.EndpointWithLostConnectionListener(s.onLostConnection))
	option = append(option, rtp.EndpointWithTrackDispatcher(s.hub))

	endpoint, err := s.rtpEngine.EstablishIngressEndpoint(s.ctx, s.Id, s.hub.LiveStreamId, *offer, option...)
	if err != nil {
		return nil, telemetry.RecordErrorf(span, "create rtp connection", err)
	}
	s.ingress = endpoint

	span.AddEvent("Wait for Local Description.")
	ctxTimeout, cancel := context.WithTimeout(ctx, iceGatheringTimeout)
	defer cancel()
	answer, err := s.ingress.GetLocalDescription(ctxTimeout)
	if err != nil {
		return nil, telemetry.RecordErrorf(span, "create egress resource endpoint", err)
	}

	span.AddEvent("Receive Local Description.")
	return answer, nil
}

func (s *Session) onLostConnection() {
	slog.Warn("session: connect lost connection", "sessionId", s.Id, "userId", s.user)
	go func() {
		select {
		case <-s.ctx.Done():
			slog.Debug("sessions: internally quit interrupted because session already closed", "session id", s.Id, "user", s.user)
		default:
			item := NewItem(s.user)
			select {
			case s.garbage <- item:
				s.stop()
				<-item.Done
				slog.Debug("sessions: internally stop of session", "session id", s.Id, "user", s.user)
			case <-s.ctx.Done():
				slog.Debug("sessions: internally stop interrupted because session already closed", "session id", s.Id, "user", s.user)
			}
		}
	}()
}

func (s *Session) trace(ctx context.Context, spanName string) (context.Context, trace.Span) {
	ctx, span := otel.Tracer(sessionTrasser).Start(ctx, "session-"+spanName, trace.WithAttributes(
		attribute.String("sessionId", s.Id.String()),
		attribute.String("userId", s.user.String()),
	))
	return ctx, span
}
