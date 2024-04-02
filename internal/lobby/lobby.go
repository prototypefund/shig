package lobby

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shigde/sfu/internal/lobby/sessions"
	"golang.org/x/exp/slog"
)

type command interface {
	GetUserId() uuid.UUID
	Execute(session *sessions.Session)
	SetError(err error)
}

var (
	ErrNoSession            = errors.New("no session exists")
	ErrSessionAlreadyExists = errors.New("session already exists")
	ErrLobbyClosed          = errors.New("lobby already closed")
)

// lobby, is a container for all sessions of a stream
type lobby struct {
	Id   uuid.UUID
	ctx  context.Context
	stop context.CancelFunc

	entity         *LobbyEntity
	hub            *sessions.Hub
	sessions       *sessions.SessionRepository
	rtp            sessions.RtpEngine
	sessionCreator chan<- sessions.Item
	sessionGarbage chan<- sessions.Item
	lobbyGarbage   chan<- lobbyItem
	cmdRunner      chan<- command
}

func newLobby(entity *LobbyEntity, rtp sessions.RtpEngine, lobbyGarbage chan<- lobbyItem) *lobby {
	ctx, stop := context.WithCancel(context.Background())
	sessRep := sessions.NewSessionRepository()
	hub := sessions.NewHub(ctx, sessRep, entity.LiveStreamId, nil)
	garbage := make(chan sessions.Item)
	creator := make(chan sessions.Item)
	runner := make(chan command)

	lob := &lobby{
		Id:   entity.UUID,
		ctx:  ctx,
		stop: stop,

		hub:            hub,
		entity:         entity,
		sessions:       sessRep,
		rtp:            rtp,
		sessionGarbage: garbage,
		sessionCreator: creator,
		lobbyGarbage:   lobbyGarbage,
		cmdRunner:      runner,
	}
	// session handling should be sequentiell to avoid data races in group state
	go func(l *lobby, sessionCreator <-chan sessions.Item, sessionGarbage chan sessions.Item, cmdRunner <-chan command) {
		for {
			select {
			case item := <-sessionCreator:
				// in the meantime the lobby could be closed, check again
				select {
				case <-l.ctx.Done():
					item.Done <- false
				default:
					session := sessions.NewSession(l.ctx, item.UserId, l.hub, l.rtp, sessionGarbage)
					ok := l.sessions.New(session)
					item.Done <- ok
				}
			case item := <-sessionGarbage:
				ok := l.sessions.DeleteByUser(item.UserId)
				item.Done <- ok
				if l.sessions.Len() == 0 {
					item := newLobbyItem(l.Id)
					go func() {
						l.lobbyGarbage <- item
					}()
					<-item.Done
					// block all callers until lobby was clean up,
					// because we want to avoid that`s callers would call more than one time to crete a session
					l.stop()
				}
			case cmd := <-cmdRunner:
				// in the meantime the lobby could be closed, check again
				select {
				case <-l.ctx.Done():
					cmd.SetError(ErrLobbyClosed)
				default:
					l.handle(cmd)
				}
			// wenn der getriggert wird können die anderen überlaufen :-(
			case <-l.ctx.Done():
				slog.Debug("stop session sequencer")
				return
			}
		}
	}(lob, creator, garbage, runner)
	return lob
}

func (l *lobby) newSession(userId uuid.UUID) bool {
	item := sessions.NewItem(userId)
	select {
	case l.sessionCreator <- item:
		ok := <-item.Done
		return ok
	case <-l.ctx.Done():
		slog.Debug("can not adding session because lobby stopped")
		return false
	case <-time.After(1 * time.Second):
		return false
	}
}

func (l *lobby) removeSession(userId uuid.UUID) bool {
	item := sessions.NewItem(userId)
	select {
	case l.sessionGarbage <- item:
		ok := <-item.Done
		return ok
	case <-l.ctx.Done():
		slog.Debug("can not remove session because lobby stopped")
		return false
	case <-time.After(10 * time.Second):
		return false
	}
}

func (l *lobby) runCommand(cmd command) {
	select {
	case l.cmdRunner <- cmd:
	case <-l.ctx.Done():
		cmd.SetError(ErrLobbyClosed)
	}
}

// handle, run session commands on existing sessions
func (l *lobby) handle(cmd command) {
	if session, found := l.sessions.FindByUserId(cmd.GetUserId()); found {
		cmd.Execute(session)
	}
	cmd.SetError(ErrNoSession)
}
