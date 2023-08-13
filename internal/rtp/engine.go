package rtp

import (
	"context"
	"fmt"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v3"
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

func (e *Engine) NewReceiverEndpoint(ctx context.Context, offer webrtc.SessionDescription, dispatcher TrackDispatcher) (*Endpoint, error) {
	_, span := otel.Tracer(tracerName).Start(ctx, "engine:create receiver-endpoint")
	defer span.End()

	peerConnection, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		return nil, fmt.Errorf("create receiver peer connection: %w ", err)
	}

	receiver := newReceiver(dispatcher)
	peerConnection.OnTrack(receiver.onTrack)

	peerConnection.OnICEConnectionStateChange(func(i webrtc.ICEConnectionState) {
		// @TODO Implement irregular connection closed by client handling
		if i == webrtc.ICEConnectionStateFailed {
			if err := peerConnection.Close(); err != nil {
				slog.Error("rtp.engine: receiver peerConnection.Close", "err", err)
			}
			receiver.stop()
		}
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		slog.Debug("rtp.engine: new DataChannel %s %d\n", d.Label(), d.ID())

		// Register channel opening handling
		d.OnOpen(func() {
			slog.Debug("rtp.engine: data channel '%s'-'%d' open. keep-alive messages will now be sent", d.Label(), d.ID())

			for range time.NewTicker(5 * time.Second).C {
				message := "keep-alive"
				sendErr := d.SendText(message)
				if sendErr != nil {
					slog.Warn("rtp.engine: data channel send", "err", sendErr)
				}
			}
		})

		// Register text message handling
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			// do something
			fmt.Printf("Message from DataChannel '%s': '%s'\n", d.Label(), string(msg.Data))
		})
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

func (e *Engine) NewSenderEndpoint(ctx context.Context, sendingTracks []*webrtc.TrackLocalStaticRTP) (*Endpoint, error) {
	_, span := otel.Tracer(tracerName).Start(ctx, "engine:create sender-endpoint")
	defer span.End()

	peerConnection, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		return nil, fmt.Errorf("create sender peer connection: %w ", err)
	}
	if sendingTracks != nil {
		for _, track := range sendingTracks {
			_, err = peerConnection.AddTrack(track)
			return nil, fmt.Errorf("adding track to connection: %w ", err)
		}
	}
	sender := newSender(peerConnection)

	peerConnection.OnICEConnectionStateChange(func(i webrtc.ICEConnectionState) {
		if i == webrtc.ICEConnectionStateFailed {
			// @TODO Implement irregular connection closed by client handling
			if err := peerConnection.Close(); err != nil {
				slog.Error("rtp.engine: sender peerConnection.Close", "err", err)
			}
			if err = sender.stop(); err != nil {
				slog.Error("rtp.engine: sender.stop", "err", err)
			}
		}
	})

	peerConnection.OnNegotiationNeeded(func() {
		_, err := peerConnection.CreateOffer(nil)
		if err != nil {
			slog.Error("rtp.engine: sender OnNegotiationNeeded", "err", err)
		}
		//send offer
	})

	creatDC(peerConnection)

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return nil, err
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
	}, nil
}

func creatDC(pc *webrtc.PeerConnection) {
	ordered := false
	maxRetransmits := uint16(0)

	options := &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	}

	// Create a datachannel with label 'data'
	dc, _ := pc.CreateDataChannel("data", options)

	var msgID = 0
	buf := make([]byte, 1000)
	// Register channel opening handling
	dc.OnOpen(func() {
		// log.Printf("OnOpen: %s-%d. Random messages will now be sent to any connected DataChannels every second\n", dc.Label(), dc.ID())

		for range time.NewTicker(1000 * time.Millisecond).C {
			// log.Printf("Sending (%d) msg with len %d \n", msgID, len(buf))
			msgID++

			_ = dc.Send(buf)

		}
	})

}
