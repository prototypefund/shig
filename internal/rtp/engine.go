package rtp

import (
	"context"
	"fmt"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v3"
	"github.com/shigde/sfu/internal/static"
	"go.opentelemetry.io/otel"
	"golang.org/x/exp/slog"
)

const tracerName = "github.com/shigde/sfu/internal/engine"

type Engine struct {
	config webrtc.Configuration
	api    *webrtc.API
}

func NewEngine(rtpConfig *RtpConfig) (*Engine, error) {
	config := rtpConfig.getWebrtcConf()

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register  default codecs: %w ", err)
	}

	// Create a InterceptorRegistry. This is the user configurable RTP/RTCP Pipeline.
	// This provides NACKs, RTCP Reports and other features. If you use `webrtc.NewPeerConnection`
	// this is enabled by default. If you are manually managing You MUST create a InterceptorRegistry
	// for each PeerConnection.
	i := &interceptor.Registry{}

	// Use the default set of Interceptors
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return nil, fmt.Errorf("register default interceptors: %w ", err)
	}

	// Register a intervalpli factory
	// This interceptor sends a PLI every 3 seconds. A PLI causes a video keyframe to be generated by the sender.
	// This makes our video seekable and more error resilent, but at a cost of lower picture quality and higher bitrates
	// A real world application should process incoming RTCP packets from viewers and forward them to senders
	intervalPliFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		return nil, fmt.Errorf("create interval Pli factory: %w ", err)
	}
	i.Add(intervalPliFactory)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))
	return &Engine{
		config: config,
		api:    api,
	}, nil
}

func (e *Engine) NewReceiverEndpoint(ctx context.Context, offer webrtc.SessionDescription, dispatcher TrackDispatcher, handler StateEventHandler) (*Endpoint, error) {
	_, span := otel.Tracer(tracerName).Start(ctx, "engine:create receiver-endpoint")
	defer span.End()

	peerConnection, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		return nil, fmt.Errorf("create receiver peer connection: %w ", err)
	}

	receiver := newReceiver(dispatcher)
	peerConnection.OnTrack(receiver.onTrack)

	peerConnection.OnICEConnectionStateChange(handler.OnConnectionStateChange)

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		slog.Debug("rtp.engine: receiverEndpoint new DataChannel", "label", d.Label(), "id", d.ID())
		handler.OnChannel(d)
	})

	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		return nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}

	return &Endpoint{
		peerConnection: peerConnection,
		receiver:       receiver,
		gatherComplete: gatherComplete,
	}, nil
}

func (e *Engine) NewSenderEndpoint(ctx context.Context, sendingTracks []*webrtc.TrackLocalStaticRTP, handler StateEventHandler) (*Endpoint, error) {
	_, span := otel.Tracer(tracerName).Start(ctx, "engine:create sender-endpoint")
	defer span.End()

	peerConnection, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		return nil, fmt.Errorf("create sender peer connection: %w ", err)
	}

	sender := newSender(peerConnection)
	peerConnection.OnICEConnectionStateChange(handler.OnConnectionStateChange)

	initComplete := make(chan struct{})

	//@TODO: Fix the race
	// First we create the sender endpoint and after this we add the individual tracks.
	// I don't know why, but Pion doesn't trigger renegotiation when creating a peer connection with tracks and the sdp
	// exchange is not finish. A peer connection without tracks where all tracks are added afterwards triggers renegotiation.
	// Unfortunately, "sendingTracks" could be outdated in the meantime.
	// This creates a race between remove and add track that I still have to think about it.
	go func() {
		<-initComplete
		if sendingTracks != nil {
			for _, track := range sendingTracks {
				if _, err = peerConnection.AddTrack(track); err != nil {
					slog.Error("rtp.engine: adding track to connection", "err", err)
				}
			}
		}
	}()

	peerConnection.OnNegotiationNeeded(func() {
		<-initComplete
		slog.Debug("rtp.engine: sender OnNegotiationNeeded was triggered")
		offer, err := peerConnection.CreateOffer(nil)
		if err != nil {
			slog.Error("rtp.engine: sender OnNegotiationNeeded", "err", err)
			return
		}
		gg := webrtc.GatheringCompletePromise(peerConnection)
		_ = peerConnection.SetLocalDescription(offer)
		<-gg
		handler.OnNegotiationNeeded(*peerConnection.LocalDescription())
	})
	slog.Debug("rtp.engine: sender: OnNegotiationNeeded setup finish")

	err = creatDC(peerConnection, handler)
	if err != nil {
		return nil, fmt.Errorf("creating data channel: %w", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return nil, fmt.Errorf("creating offer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		return nil, err
	}

	return &Endpoint{
		peerConnection: peerConnection,
		sender:         sender,
		AddTrackChan:   sender.addTrackChan,
		gatherComplete: gatherComplete,
		initComplete:   initComplete,
	}, nil
}

func creatDC(pc *webrtc.PeerConnection, handler StateEventHandler) error {
	ordered := false
	maxRetransmits := uint16(0)

	options := &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	}

	// Create a datachannel with label 'data'
	dc, err := pc.CreateDataChannel("data", options)
	if err != nil {
		return fmt.Errorf("creating data channel: %w", err)
	}
	handler.OnChannel(dc)
	return nil
}

func (e *Engine) NewMediaSenderEndpoint(media *static.MediaFile) (*Endpoint, error) {
	stateHandler := newMediaStateEventHandler()
	peerConnection, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		return nil, fmt.Errorf("create receiver peer connection: %w ", err)
	}

	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
		}
	})

	rtpVideoSender, err := peerConnection.AddTrack(media.VideoTrack)
	if err != nil {
		return nil, fmt.Errorf("add video track to peer connection: %w ", err)
	}
	media.PlayVideo(iceConnectedCtx, rtpVideoSender)

	rtpAudioSender, err := peerConnection.AddTrack(media.AudioTrack)
	if err != nil {
		return nil, fmt.Errorf("add audio track to peer connection: %w ", err)
	}
	media.PlayAudio(iceConnectedCtx, rtpAudioSender)

	err = creatDC(peerConnection, stateHandler)

	if err != nil {
		return nil, fmt.Errorf("creating data channel: %w", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return nil, fmt.Errorf("creating offer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		return nil, err
	}

	return &Endpoint{peerConnection: peerConnection, gatherComplete: gatherComplete}, nil
}
