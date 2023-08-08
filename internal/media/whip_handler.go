package media

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"strconv"

	"github.com/shigde/sfu/internal/auth"
)

func whip(spaceManager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/sdp")

		if err := auth.StartSession(w, r); err != nil {
			httpError(w, "error", http.StatusInternalServerError, err)
		}

		liveStream, space, err := getLiveStream(r, spaceManager)
		if err != nil {
			handleResourceError(w, err)
			return
		}

		offer, err := getSdpPayload(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		user, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		userId, err := user.GetUuid()
		if err != nil {
			httpError(w, "error user", http.StatusBadRequest, err)
			return
		}
		auth.SetNewRequestToken(w, user.UUID)

		answer, resourceId, err := space.EnterLobby(r.Context(), offer, liveStream, userId)
		if err != nil {
			httpError(w, "error build whip", http.StatusInternalServerError, err)
			return
		}

		response := []byte(answer.SDP)
		hash := md5.Sum(response)

		w.WriteHeader(http.StatusCreated)
		w.Header().Set("etag", fmt.Sprintf("%x", hash))
		w.Header().Set("Location", "resource/"+resourceId)
		contentLen, err := w.Write(response)
		if err != nil {
			httpError(w, "error build response", http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(contentLen))
	}
}
