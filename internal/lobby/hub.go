package lobby

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shigde/sfu/internal/metric"
	"github.com/shigde/sfu/internal/rtp"
	"golang.org/x/exp/slog"
)

var (
	errHubAlreadyClosed   = errors.New("hub was already closed")
	errHubDispatchTimeOut = errors.New("hub dispatch timeout")
	hubDispatchTimeout    = 3 * time.Second
)

type hub struct {
	LiveStreamId  uuid.UUID
	sessionRepo   *sessionRepository
	sender        liveStreamSender
	reqChan       chan *hubRequest
	tracks        map[string]*rtp.TrackInfo   // trackID --> TrackInfo
	metricNodes   map[string]metric.GraphNode // sessionId --> metric Node
	hubMetricNode metric.GraphNode
	quit          chan struct{}
}

func newHub(sessionRepo *sessionRepository, liveStream uuid.UUID, sender liveStreamSender, quit chan struct{}) *hub {
	tracks := make(map[string]*rtp.TrackInfo)
	metricNodes := make(map[string]metric.GraphNode)
	requests := make(chan *hubRequest)
	hubMetricNode := metric.GraphNodeUpdate(metric.BuildNode(liveStream.String(), liveStream.String(), "hub"))
	hub := &hub{
		liveStream,
		sessionRepo,
		sender,
		requests,
		tracks,
		metricNodes,
		hubMetricNode,
		quit,
	}
	go hub.run()

	return hub
}

func (h *hub) run() {
	slog.Info("lobby.hub: run")
	for {
		select {
		case trackEvent := <-h.reqChan:
			switch trackEvent.kind {
			case addTrack:
				h.onAddTrack(trackEvent)
			case removeTrack:
				h.onRemoveTrack(trackEvent)
			case getTrackList:
				h.onGetTrackList(trackEvent)
			}
		case <-h.quit:
			slog.Info("lobby.hub: closed hub")
			return
		}
	}
}

func (h *hub) DispatchAddTrack(track *rtp.TrackInfo) {
	select {
	case h.reqChan <- &hubRequest{kind: addTrack, track: track}:
		slog.Debug("lobby.hub: dispatch add track", "streamId", track.GetTrackLocal().StreamID(), "track", track.GetTrackLocal().ID(), "kind", track.GetTrackLocal().Kind(), "purpose", track.Purpose.ToString())
	case <-h.quit:
		slog.Warn("lobby.hub: dispatch add track even on closed hub")
	case <-time.After(hubDispatchTimeout):
		slog.Error("lobby.hub: dispatch add track - interrupted because dispatch timeout")
	}
}

func (h *hub) DispatchRemoveTrack(track *rtp.TrackInfo) {
	select {
	case h.reqChan <- &hubRequest{kind: removeTrack, track: track}:
		slog.Debug("lobby.hub: dispatch remove track", "streamId", track.GetTrackLocal().StreamID(), "track", track.GetTrackLocal().ID(), "kind", track.GetTrackLocal().Kind(), "purpose", track.Purpose.ToString())
	case <-h.quit:
		slog.Warn("lobby.hub: dispatch remove track even on closed hub")
	case <-time.After(hubDispatchTimeout):
		slog.Error("lobby.hub: dispatch remove track - interrupted because dispatch timeout")
	}
}

// getTrackList Is called from the Egress endpoints when the connection is established.
// In ths wax the egress endpoints can receive the current tracks of the lobby
// The session set this methode as callback to the egress endpoint
func (h *hub) getTrackList(sessionId uuid.UUID, filters ...filterHubTracks) ([]*rtp.TrackInfo, error) {
	var hubList []*rtp.TrackInfo
	trackListChan := make(chan []*rtp.TrackInfo)
	select {
	case h.reqChan <- &hubRequest{kind: getTrackList, trackListChan: trackListChan}:
	case <-h.quit:
		slog.Warn("lobby.hub: get track list on closed hub")
		return nil, errHubAlreadyClosed
	case <-time.After(hubDispatchTimeout):
		slog.Error("lobby.hub: get track list - interrupted because dispatch timeout")
		return nil, errHubDispatchTimeOut
	}

	select {
	case hubList = <-trackListChan:
	case <-h.quit:
		slog.Warn("lobby.hub: get track list on closed hub")
	case <-time.After(hubDispatchTimeout):
		slog.Error("lobby.hub: get track list - interrupted because dispatch timeout")
	}
	list := make([]*rtp.TrackInfo, 0)
	for _, track := range hubList {
		// If we have no filter, we can add the track to the list
		if len(filters) == 0 {
			list = append(list, track)
			h.increaseNodeGraphStats(sessionId.String(), rtp.EgressEndpoint, track.Purpose)
			continue
		}

		// Check filter
		canAddTrackToList := true
		for _, f := range filters {
			// If one filter not be true we will not add the track to the list
			if !f(track) {
				canAddTrackToList = false
				break
			}
		}

		if canAddTrackToList {
			list = append(list, track)
			h.increaseNodeGraphStats(sessionId.String(), rtp.EgressEndpoint, track.Purpose)
		}
	}
	return list, nil
}

func (h *hub) stop() error {
	slog.Info("lobby.hub: stop")
	select {
	case <-h.quit:
		slog.Error("lobby.sessions: the hub was already closed")
		return errHubAlreadyClosed
	default:
		close(h.quit)
		slog.Info("lobby.hub: stopped was triggered")
		<-h.quit
		metric.GraphNodeDelete(h.hubMetricNode)
	}
	return nil
}

func (h *hub) onAddTrack(event *hubRequest) {

	h.increaseNodeGraphStats(event.track.SessionId.String(), rtp.IngressEndpoint, event.track.Purpose)
	h.hubMetricNode = metric.GraphNodeUpdateInc(h.hubMetricNode, event.track.Purpose.ToString())
	if event.track.GetPurpose() == rtp.PurposeMain {
		slog.Debug("lobby.hub: add live track ro sender", "streamId", event.track.GetTrackLocal().StreamID(), "track", event.track.GetTrackLocal().ID(), "kind", event.track.GetTrackLocal().Kind(), "purpose", event.track.Purpose.ToString())
		h.sender.AddTrack(event.track.GetTrackLocal())
	}

	h.tracks[event.track.GetTrackLocal().ID()] = event.track
	h.sessionRepo.Iter(func(s *session) {
		if filterForSession(s.Id)(event.track) {
			slog.Debug("lobby.hub: add egress track to session", "session", s.Id, "trackSession", event.track.SessionId, "streamId", event.track.GetTrackLocal().StreamID(), "track", event.track.GetTrackLocal().ID(), "kind", event.track.GetTrackLocal().Kind(), "purpose", event.track.Purpose.ToString())
			s.addTrack(event.track)
			h.increaseNodeGraphStats(s.Id.String(), rtp.EgressEndpoint, event.track.Purpose)
		}
	})
}

func (h *hub) onRemoveTrack(event *hubRequest) {
	h.hubMetricNode = metric.GraphNodeUpdateDec(h.hubMetricNode, event.track.Purpose.ToString())
	h.decreaseNodeGraphStats(event.track.SessionId.String(), rtp.IngressEndpoint, event.track.Purpose)

	if event.track.GetPurpose() == rtp.PurposeMain {
		h.sender.RemoveTrack(event.track.GetTrackLocal())
	}

	if _, ok := h.tracks[event.track.GetTrackLocal().ID()]; ok {
		delete(h.tracks, event.track.GetTrackLocal().ID())
	}
	h.sessionRepo.Iter(func(s *session) {
		if filterForSession(s.Id)(event.track) {
			s.removeTrack(event.track)
			h.decreaseNodeGraphStats(s.Id.String(), rtp.EgressEndpoint, event.track.Purpose)
		}
	})
}

func (h *hub) onGetTrackList(event *hubRequest) {
	list := make([]*rtp.TrackInfo, 0, len(h.tracks))
	for _, track := range h.tracks {
		list = append(list, track)
	}

	select {
	case event.trackListChan <- list:
	case <-h.quit:
		slog.Warn("lobby.hub: onGetTrackList on closed hub")
	case <-time.After(hubDispatchTimeout):
		slog.Error("lobby.hub: onGetTrackList - interrupted because dispatch timeout")
	}
}

func (h *hub) increaseNodeGraphStats(sessionId string, endpointType rtp.EndpointType, purpose rtp.Purpose) {
	index := endpointType.ToString() + sessionId
	metricNode, ok := h.metricNodes[index]
	if !ok {
		// if metric not found create the endpoint and the edge from endpoint to lobby
		metricNode = metric.BuildNode(sessionId, h.LiveStreamId.String(), endpointType.ToString())
		metric.GraphAddEdge(sessionId, h.LiveStreamId.String(), endpointType.ToString())
	}
	h.metricNodes[index] = metric.GraphNodeUpdateInc(metricNode, purpose.ToString())
}

func (h *hub) decreaseNodeGraphStats(sessionId string, endpointType rtp.EndpointType, purpose rtp.Purpose) {
	index := endpointType.ToString() + sessionId
	if metricNode, ok := h.metricNodes[index]; ok {
		metricNode = metric.GraphNodeUpdateDec(metricNode, purpose.ToString())
		if metricNode.Tracks == 0 && metricNode.MainTracks == 0 {
			metric.GraphDeleteEdge(sessionId, h.LiveStreamId.String(), endpointType.ToString())
			delete(h.metricNodes, index)
			return
		}
		h.metricNodes[index] = metricNode
	}
}

type filterHubTracks func(*rtp.TrackInfo) bool

func filterForSession(sessionId uuid.UUID) filterHubTracks {
	return func(track *rtp.TrackInfo) bool {
		return sessionId.String() != track.GetSessionId().String()
	}
}
func filterForNotMain() filterHubTracks {
	return func(track *rtp.TrackInfo) bool {
		return track.Purpose != rtp.PurposeMain
	}
}
