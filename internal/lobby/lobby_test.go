package lobby

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shigde/sfu/internal/logging"
	"github.com/stretchr/testify/assert"
)

func testStreamLobbySetup(t *testing.T) (*lobby, uuid.UUID) {
	t.Helper()
	logging.SetupDebugLogger()
	// set one session in lobby
	engine := mockRtpEngineForOffer(mockedAnswer)
	entity := &LobbyEntity{
		UUID:         uuid.New(),
		LiveStreamId: uuid.New(),
		Space:        "space",
		Host:         "http://localhost:1234/federation/accounts/shig-test",
	}

	hostSettings := hostInstanceSettings{
		isHost: true,
		url:    nil,
		token:  "token",
		space:  entity.Space,
		stream: entity.LiveStreamId.String(),
	}

	lobby := newLobby(entity.UUID, entity, engine, make(chan uuid.UUID), hostSettings)
	user := uuid.New()
	session := newSession(user, lobby.hub, engine, lobby.sessionQuit)
	session.signal.messenger = newMockedMessenger(t)
	session.ingress = mockConnection(mockedAnswer)

	session.egress = mockConnection(mockedAnswer)
	session.signal.egressEndpoint = session.egress
	lobby.sessions.Add(session)
	return lobby, user
}
func TestStreamLobby(t *testing.T) {

	t.Run("new ingress endpoint", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		request := newLobbyRequest(context.Background(), uuid.New())
		joinData := newIngressEndpointData(mockedOffer)
		request.data = joinData

		go lobby.runRequest(request)

		select {
		case data := <-joinData.response:
			assert.Equal(t, mockedAnswer, data.answer)
			assert.False(t, uuid.Nil == data.resource)
			assert.False(t, uuid.Nil == data.RtpSessionId)
		case <-time.After(time.Second * 3):
			t.Fail()
		}
	})

	t.Run("cancel new ingress endpoint req lobby", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		ctx, cancel := context.WithCancel(context.Background())
		request := newLobbyRequest(ctx, uuid.New())
		joinData := newIngressEndpointData(mockedOffer)
		request.data = joinData

		cancel()
		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errLobbyRequestTimeout)
		case <-time.After(time.Second * 3):
			t.Fail()
		}
	})

	t.Run("finally create new egress endpoint in lobby", func(t *testing.T) {
		lobby, user := testStreamLobbySetup(t)
		defer lobby.stop()
		request := newLobbyRequest(context.Background(), user)
		listenData := newFinalCreateEgressEndpointData(mockedOffer)
		request.data = listenData

		go lobby.runRequest(request)

		select {
		case data := <-listenData.response:
			assert.False(t, uuid.Nil == data.RtpSessionId)
		case <-time.After(time.Second * 3):
			t.Fail()
		}
	})

	t.Run("cancel req for finally create new egress endpoint in lobby", func(t *testing.T) {
		lobby, user := testStreamLobbySetup(t)
		defer lobby.stop()

		ctx, cancel := context.WithCancel(context.Background())
		request := newLobbyRequest(ctx, user)
		listenData := newFinalCreateEgressEndpointData(mockedAnswer)
		request.data = listenData

		cancel()
		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errLobbyRequestTimeout)
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("init egress endpoint but no session was started before", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		request := newLobbyRequest(context.Background(), uuid.New())
		startData := newInitEgressEndpointData()
		request.data = startData

		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errNoSession)
		case _ = <-startData.response:
			t.Fatalf("test fails because no offer expected")
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("init egress endpoint", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		user := uuid.New()
		session := newSession(user, lobby.hub, mockRtpEngineForOffer(mockedAnswer), lobby.sessionQuit)
		session.ingress = mockConnection(nil)
		session.signal.messenger = newMockedMessenger(t)
		session.signal.stopWaitingForMessenger()
		lobby.sessions.Add(session)

		request := newLobbyRequest(context.Background(), user)
		startData := newInitEgressEndpointData()
		request.data = startData

		go lobby.runRequest(request)
		offer := mockedAnswer // its mocked and make no different

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errNoSession)
		case data := <-startData.response:
			assert.Equal(t, offer, data.offer)
			assert.False(t, uuid.Nil == data.RtpSessionId)
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("cancel init egress endpoint lobby req", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		user := uuid.New()
		session := newSession(user, lobby.hub, mockRtpEngineForOffer(mockedAnswer), lobby.sessionQuit)
		session.signal.messenger = newMockedMessenger(t)
		lobby.sessions.Add(session)

		ctx, cancel := context.WithCancel(context.Background())
		request := newLobbyRequest(ctx, user)
		startData := newInitEgressEndpointData()
		request.data = startData

		cancel()
		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errLobbyRequestTimeout)
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("stop a session internally", func(t *testing.T) {
		lobby, user := testStreamLobbySetup(t)
		defer lobby.stop()
		session, _ := lobby.sessions.FindByUserId(user)

		stopped, _ := lobby.deleteSessionByUserId(user)
		assert.True(t, stopped)
		assert.False(t, lobby.sessions.Contains(session.Id))
	})

	t.Run("leave a lobby", func(t *testing.T) {
		lobby, user := testStreamLobbySetup(t)
		defer lobby.stop()
		request := newLobbyRequest(context.Background(), user)
		leaveData := newLeaveData()
		request.data = leaveData

		go lobby.runRequest(request)

		select {
		case success := <-leaveData.response:
			assert.True(t, success)
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("create static egress endpoint", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		user := uuid.New()
		request := newLobbyRequest(context.Background(), user)
		egressData := newMainEgressEndpointData(mockedOffer)
		request.data = egressData

		go lobby.runRequest(request)
		answer := mockedAnswer

		select {
		case err := <-request.err:
			t.Fatalf("test fails because an error: %v", err)
		case data := <-egressData.response:
			assert.Equal(t, answer, data.answer)
			assert.False(t, uuid.Nil == data.RtpSessionId)
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("create static egress endpoint, but endpoint already exits", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		user := uuid.New()
		// Creat a session to simulate existing endpoint
		session := newSession(user, nil, nil, nil)
		lobby.sessions.Add(session)

		request := newLobbyRequest(context.Background(), user)
		egressData := newMainEgressEndpointData(mockedOffer)
		request.data = egressData

		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, ErrSessionAlreadyExists)
		case _ = <-egressData.response:
			t.Fatalf("test fails because an error is expected")
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})

	t.Run("cancel request to create static egress endpoint", func(t *testing.T) {
		lobby, _ := testStreamLobbySetup(t)
		defer lobby.stop()

		user := uuid.New()
		ctx, cancel := context.WithCancel(context.Background())
		request := newLobbyRequest(ctx, user)
		egressData := newMainEgressEndpointData(mockedOffer)
		request.data = egressData

		cancel()
		go lobby.runRequest(request)

		select {
		case err := <-request.err:
			assert.ErrorIs(t, err, errLobbyRequestTimeout)
		case _ = <-egressData.response:
			t.Fatalf("test fails because an error is expected")
		case <-time.After(time.Second * 3):
			t.Fatalf("test fails because run in timeout")
		}
	})
}
