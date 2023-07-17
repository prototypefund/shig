package media

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/shigde/sfu/internal/auth"
	"github.com/shigde/sfu/internal/stream"
)

func getStreamList(manager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		space, err := getSpace(r, manager)
		if err != nil {
			handleResourceError(w, err)
			return
		}
		streams, err := space.LiveStreamRepo.All(r.Context())
		if err != nil {
			httpError(w, "error reading stream list", http.StatusInternalServerError, err)
			return
		}

		if err := json.NewEncoder(w).Encode(streams); err != nil {
			httpError(w, "error reading stream list", http.StatusInternalServerError, err)
		}
	}
}
func getStream(manager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		streamResource, _, err := getLiveStream(r, manager)
		if err != nil {
			handleResourceError(w, err)
			return
		}

		if err := json.NewEncoder(w).Encode(streamResource); err != nil {
			httpError(w, "stream invalid", http.StatusInternalServerError, err)
		}
	}
}

func deleteStream(manager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		space, err := getSpace(r, manager)
		if err != nil {
			handleResourceError(w, err)
			return
		}

		id, ok := mux.Vars(r)["id"]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := space.LiveStreamRepo.Delete(r.Context(), id); err != nil {
			httpError(w, "error delete stream", http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func createStream(manager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		space, err := getOrCreateSpace(r, manager)
		if err != nil {
			handleResourceError(w, err)
			return
		}

		var liveStream stream.LiveStream
		if err := getStreamResourcePayload(w, r, &liveStream); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		liveStream.User = user.UUID
		liveStream.SpaceId = space.Id
		id, err := space.LiveStreamRepo.Add(r.Context(), &liveStream)
		if err != nil {
			httpError(w, "error create stream", http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Location", fmt.Sprintf("%s/%s", r.URL.String(), id))
		w.WriteHeader(http.StatusCreated)
	}
}

func updateStream(manager spaceGetCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		space, err := getSpace(r, manager)
		if err != nil {
			handleResourceError(w, err)
			return
		}

		var liveStream stream.LiveStream
		if err := getStreamResourcePayload(w, r, &liveStream); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := space.LiveStreamRepo.Update(r.Context(), &liveStream); err != nil {
			httpError(w, "error update stream", http.StatusInternalServerError, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func getStreamResourcePayload(w http.ResponseWriter, r *http.Request, liveStream *stream.LiveStream) error {
	dec, err := getJsonPayload(w, r)
	if err != nil {
		return err
	}

	if err := dec.Decode(&liveStream); err != nil {
		return invalidPayload
	}

	return nil
}