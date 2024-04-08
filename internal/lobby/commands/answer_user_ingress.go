package commands

import (
	"context"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"github.com/shigde/sfu/internal/lobby/resources"
	"github.com/shigde/sfu/internal/lobby/sessions"
)

type AnswerUserIngress struct {
	*Command
	sdp      *webrtc.SessionDescription
	option   []resources.Option
	Response *resources.WebRTC
}

func NewAnswerUserIngress(
	ctx context.Context,
	user uuid.UUID,
	sdp *webrtc.SessionDescription,
	option ...resources.Option,
) *AnswerUserIngress {
	command := NewCommand(ctx, user)
	return &AnswerUserIngress{
		Command:  command,
		sdp:      sdp,
		option:   option,
		Response: nil,
	}
}
func (c *AnswerUserIngress) Execute(session *sessions.Session) {
	answer, err := session.CreateIngressEndpoint(c.ParentCtx, c.sdp)
	if err != nil {
		c.SetError(err)
		return
	}

	c.Response = &resources.WebRTC{
		Id:  session.Id.String(),
		SDP: answer,
	}
	c.SetDone()
}
