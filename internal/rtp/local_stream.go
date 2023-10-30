package rtp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"go.opentelemetry.io/otel"
	"golang.org/x/exp/slog"
)

const rtpBufferSize = 1500

type localStream struct {
	id                       uuid.UUID
	remoteId                 string
	sessionId                uuid.UUID
	audioTrack, videoTrack   *webrtc.TrackLocalStaticRTP
	audioWriter, videoWriter *localWriter
	kind                     TrackInfoKind
	dispatcher               TrackDispatcher
	globalQuit               <-chan struct{}
}

func newLocalStream(remoteId string, sessionId uuid.UUID, dispatcher TrackDispatcher, kind TrackInfoKind, globalQuit <-chan struct{}) *localStream {
	return &localStream{
		id:         uuid.New(),
		remoteId:   remoteId,
		sessionId:  sessionId,
		dispatcher: dispatcher,
		kind:       kind,
		globalQuit: globalQuit,
	}
}

func (s *localStream) writeAudioRtp(ctx context.Context, track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) error {
	slog.Debug("rtp.receiver: on track")
	ctx, span := otel.Tracer(tracerName).Start(ctx, "localStream:writeAudioRtp")
	defer span.End()
	audio, err := s.createNewAudioLocalTrack(track)
	if err != nil {
		return fmt.Errorf("adding audio remote track (%s:%s) to local stream: %w", track.ID(), track.StreamID(), err)
	}
	s.audioTrack = audio
	s.audioWriter = newLocalWriter(s.audioTrack.ID(), s.globalQuit)

	// start local audio track
	go func() {
		defer s.dispatcher.DispatchRemoveTrack(newTrackInfo(s.sessionId, s.getAudioTrack(), s.getLiveAudioTrack(), s.kind))

		if err = s.audioWriter.writeRtp(track, s.audioTrack); err != nil {
			slog.Error("rtp.local_stream: writing local audio track ", "streamId", s.id, "err", err)
		}
		slog.Debug("rtp.local_stream: stop writing local audio track ", "streamId", s.id, "trackId", s.audioTrack.ID())
	}()
	return nil
}

func (s *localStream) writeVideoRtp(ctx context.Context, track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) error {
	_, span := otel.Tracer(tracerName).Start(ctx, "localStream:writeVideoRtp")
	defer span.End()

	video, err := s.createNewVideoLocalTrack(track)
	if err != nil {
		return fmt.Errorf("adding video remote track (%s:%s) to local stream: %w", track.ID(), track.StreamID(), err)
	}

	s.videoTrack = video
	s.videoWriter = newLocalWriter(s.videoTrack.ID(), s.globalQuit)

	// start local video track
	go func() {
		defer s.dispatcher.DispatchRemoveTrack(newTrackInfo(s.sessionId, s.getVideoTrack(), s.getLiveVideoTrack(), s.kind))

		if err = s.videoWriter.writeRtp(track, s.videoTrack); err != nil {
			slog.Error("rtp.local_stream: writing local video track ", "streamId", s.id, "err", err)
		}
		slog.Debug("rtp.local_stream: stop writing local video track ", "streamId", s.id, "trackId", s.videoTrack.ID())
	}()

	return nil
}

func (s *localStream) createNewAudioLocalTrack(remoteTrack *webrtc.TrackRemote) (*webrtc.TrackLocalStaticRTP, error) {
	if s.audioTrack != nil {
		return nil, errors.New("has already audio track")
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability, uuid.NewString(), s.id.String())
	if err != nil {
		return nil, fmt.Errorf("creating new local audio track for local stream %s: %w", s.id, err)
	}
	return audio, nil
}

func (s *localStream) createNewVideoLocalTrack(remoteTrack *webrtc.TrackRemote) (*webrtc.TrackLocalStaticRTP, error) {
	if s.videoTrack != nil {
		return nil, errors.New("has already video track")
	}
	video, err := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability, uuid.NewString(), s.id.String())
	if err != nil {
		return nil, fmt.Errorf("creating new local video track for local stream %s: %w", s.id, err)
	}
	return video, nil
}

func (s *localStream) close() {
	slog.Debug("rtp.local_stream: the stream shutdown", "streamId", s.id)
	if s.audioWriter != nil && s.audioWriter.isRunning() {
		s.audioWriter.close()
	}
	if s.videoWriter != nil && s.videoWriter.isRunning() {
		s.videoWriter.close()
	}
}

func (s *localStream) getVideoTrack() *webrtc.TrackLocalStaticRTP {
	return s.videoTrack
}

func (s *localStream) getAudioTrack() *webrtc.TrackLocalStaticRTP {
	return s.audioTrack
}

func (s *localStream) getLiveVideoTrack() *LiveTrackStaticRTP {
	return nil
}

func (s *localStream) getLiveAudioTrack() *LiveTrackStaticRTP {
	return nil
}

func (s *localStream) getKind() TrackInfoKind {
	return s.kind
}
